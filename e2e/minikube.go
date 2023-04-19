// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/stretchr/testify/require"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	configMutex sync.Mutex
	runtimes    = []types.Runtime{types.RuntimeContainerd, types.RuntimeCrio, types.RuntimeDocker}
)

type Minikube struct {
	runtime types.Runtime
	profile string
	stdout  io.Writer
	stderr  io.Writer

	clientOnce sync.Once
	client     *kubernetes.Clientset
	config     *rest.Config
}

func startMinikube(runtime types.Runtime) (*Minikube, error) {
	stdout := prefixWriter{prefix: "🧊", w: os.Stdout}
	stderr := prefixWriter{prefix: "🧊", w: os.Stderr}

	configMutex.Lock()
	defer configMutex.Unlock()
	profile := "e2e-" + string(runtime)

	_ = exec.Command("minikube", "-p", profile, "delete").Run()

	args := []string{"-p", profile, "start", "--keep-context", fmt.Sprintf("--container-runtime=%s", string(runtime)), "--ports=8080"}
	if runtime == "cri-o" {
		args = append(args, "--cni=bridge")
	}
	cmd := exec.Command("minikube", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return &Minikube{runtime: runtime, profile: profile}, nil
}

func (m *Minikube) Client() *kubernetes.Clientset {
	if m.client == nil {
		m.clientOnce.Do(func() {
			client, config, err := createKubernetesClient(m.profile)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to create kubernetes client")
			}
			m.client = client
			m.config = config
		})
	}
	return m.client
}

func (m *Minikube) Config() *rest.Config {
	if m.config == nil {
		m.Client()
	}
	return m.config
}

func (m *Minikube) Runtime() types.Runtime {
	return m.runtime
}

func (m *Minikube) waitForDefaultServiceaccount() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return errors.New("the serviceaccount 'default' was not created")
		case <-time.After(200 * time.Millisecond):
			if _, err := m.Client().CoreV1().ServiceAccounts("default").Get(context.Background(), "default", metav1.GetOptions{}); err == nil {
				return nil
			}
		}
	}
}

func (m *Minikube) delete() error {
	configMutex.Lock()
	defer configMutex.Unlock()
	cmd := exec.Command("minikube", "-p", m.profile, "delete")
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	return cmd.Run()
}

func (m *Minikube) cp(src, dst string) error {
	cmd := exec.Command("minikube", "-p", m.profile, "cp", src, dst)
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	return cmd.Run()
}

func (m *Minikube) exec(arg ...string) *exec.Cmd {
	cmd := exec.Command("minikube", append([]string{"-p", m.profile, "ssh", "--"}, arg...)...)
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	return cmd
}

func (m *Minikube) LoadImage(image string) error {
	cmd := exec.Command("minikube", "-p", m.profile, "image", "load", image)
	cmd.Stdout = m.stdout
	cmd.Stderr = m.stderr
	return cmd.Run()
}

type WithMinikubeTestCase struct {
	Name string
	Test func(t *testing.T, minikube *Minikube, e *Extension)
}

func WithMinikube(t *testing.T, testCases []WithMinikubeTestCase) {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	imageName := ""
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		s, err := createExtensionContainer()
		if err != nil {
			t.Fatalf("failed to create extension executable: %v", err)
		}
		imageName = s
		wg.Done()
	}()

	for _, runtime := range runtimes {
		t.Run(string(runtime), func(t *testing.T) {

			minikube, err := startMinikube(runtime)
			if err != nil {
				t.Fatalf("failed to start minikube: %v", err)
			}
			defer func() { _ = minikube.delete() }()

			t.Parallel()

			if err := minikube.waitForDefaultServiceaccount(); err != nil {
				t.Fatal("Serviceaccount didn't show up", err)
			}

			wg.Wait()
			extension, err := startExtension(minikube, imageName)
			require.NoError(t, err)
			defer func() { _ = extension.stop() }()

			for _, tc := range testCases {
				t.Run(tc.Name, func(t *testing.T) {
					tc.Test(t, minikube, extension)
				})
			}
		})
	}
}

func createKubernetesClient(context string) (*kubernetes.Clientset, *rest.Config, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: filepath.Join(homedir.HomeDir(), ".kube", "config")},
		&clientcmd.ConfigOverrides{CurrentContext: context},
	).ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	return client, config, err
}

type prefixWriter struct {
	prefix string
	w      io.Writer
}

func (w *prefixWriter) Write(p []byte) (n int, err error) {
	lines := strings.Split(strings.TrimSuffix(string(p), "\n"), "\n")
	count := 0
	for _, line := range lines {
		c, err := fmt.Fprintf(w.w, "%s%s\n", w.prefix, line)
		count += c
		if err != nil {
			return count, err
		}
	}
	return len(p), nil
}
