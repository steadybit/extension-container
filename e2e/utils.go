// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/extcontainer"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	ametav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"strings"
	"testing"
	"time"
)

func assertProcessRunningInContainer(t *testing.T, m *Minikube, podname, containername string, comm string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lastOutput := ""
	for {
		select {
		case <-ctx.Done():
			assert.Failf(t, "process not found", "process %s not found in container %s/%s.\n%s", comm, podname, containername, lastOutput)
			return

		case <-time.After(200 * time.Millisecond):
			req := m.Client().CoreV1().RESTClient().Post().
				Namespace("default").
				Resource("pods").
				Name(podname).
				SubResource("exec").
				VersionedParams(&corev1.PodExecOptions{
					Container: containername,
					Command:   []string{"ps", "-opid,comm"},
					Stdout:    true,
					Stderr:    true,
					TTY:       true,
				}, scheme.ParameterCodec)

			exec, err := remotecommand.NewSPDYExecutor(m.Config(), "POST", req.URL())
			require.NoError(t, err)

			var outb bytes.Buffer
			err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
				Stdout: &outb,
				Stderr: &outb,
				Tty:    true,
			})
			require.NoError(t, err, "failed to exec ps -o=pid,comm: %s", outb.String())

			for _, line := range strings.Split(outb.String(), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[1] == comm {
					return
				}
			}
			lastOutput = outb.String()
		}
	}
}

func waitForPodPhase(m *Minikube, pod metav1.Object, phase corev1.PodPhase, duration time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var lastStatus corev1.PodPhase
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("pod %s/%s did not reach phase %s. last status %s", pod.GetNamespace(), pod.GetName(), phase, lastStatus)
		case <-time.After(200 * time.Millisecond):
			p, err := m.Client().CoreV1().Pods(pod.GetNamespace()).Get(context.Background(), pod.GetName(), metav1.GetOptions{})
			if err == nil && p.Status.Phase == phase {
				return nil
			}
			lastStatus = p.Status.Phase
		}
	}
}

func getContainerStatus(m *Minikube, pod metav1.Object, containerName string) (*corev1.ContainerStatus, error) {
	r, err := m.Client().CoreV1().Pods(pod.GetNamespace()).Get(context.Background(), pod.GetName(), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	for _, status := range r.Status.ContainerStatuses {
		if status.Name == containerName {
			return &status, nil
		}
	}
	return nil, nil
}

func createBusyBoxPod(m *Minikube, podName string) (metav1.Object, error) {
	pod := &acorev1.PodApplyConfiguration{
		TypeMetaApplyConfiguration: ametav1.TypeMetaApplyConfiguration{
			Kind:       extutil.Ptr("Pod"),
			APIVersion: extutil.Ptr("v1"),
		},
		ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
			Name: &podName,
		},
		Spec: &acorev1.PodSpecApplyConfiguration{
			RestartPolicy: extutil.Ptr(corev1.RestartPolicyNever),
			Containers: []acorev1.ContainerApplyConfiguration{
				{
					Name:  extutil.Ptr("busybox"),
					Image: extutil.Ptr("busybox:1"),
					Args:  []string{"sleep", "600"},
				},
			},
		},
		Status: nil,
	}

	applied, err := m.Client().CoreV1().Pods("default").Apply(context.Background(), pod, metav1.ApplyOptions{FieldManager: "application/apply-patch"})
	if err != nil {
		return nil, err
	}
	if err = waitForPodPhase(m, applied.GetObjectMeta(), corev1.PodRunning, 30*time.Second); err != nil {
		return nil, err
	}
	return applied.GetObjectMeta(), nil
}

func deletePod(m *Minikube, pod metav1.Object) error {
	if pod == nil {
		return nil
	}
	return m.Client().CoreV1().Pods(pod.GetNamespace()).Delete(context.Background(), pod.GetName(), metav1.DeleteOptions{GracePeriodSeconds: extutil.Ptr(int64(0))})
}

func waitForContainerStatusUsingContainerEngine(m *Minikube, containerId string, wantedStatus string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var lastError error
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("container %s did not reach status %s. last error %w", containerId, wantedStatus, lastError)
		case <-time.After(200 * time.Millisecond):
			status, err := getContainerStatusUsingContainerEngine(m, containerId)
			if err != nil {
				lastError = err
			} else {
				if status == wantedStatus {
					return nil
				}
			}
		}
	}
}

func getContainerStatusUsingContainerEngine(m *Minikube, containerId string) (string, error) {
	if strings.HasPrefix(containerId, string(types.RuntimeDocker)) {
		var outb bytes.Buffer
		cmd := m.exec("sudo docker", "inspect", "-f='{{.State.Status}}'", extcontainer.RemovePrefix(containerId))
		cmd.Stdout = &outb
		if err := cmd.Run(); err != nil {
			return "", err
		}
		return strings.TrimSpace(outb.String()), nil
	}

	if strings.HasPrefix(containerId, string(types.RuntimeContainerd)) {
		var outb bytes.Buffer
		cmd := m.exec("sudo ctr", "--namespace=k8s.io", "tasks", "list")
		cmd.Stdout = &outb
		if err := cmd.Run(); err != nil {
			return "", err
		}

		for _, line := range strings.Split(outb.String(), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[0] == extcontainer.RemovePrefix(containerId) {
				return strings.ToLower(fields[2]), nil
			}
		}
		return "", fmt.Errorf("container not found container runtime")
	}

	return "", fmt.Errorf("unsupported container runtime")
}

func pollForTarget(ctx context.Context, e *Extension, predicate func(target discovery_kit_api.Target) bool) (discovery_kit_api.Target, error) {
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return discovery_kit_api.Target{}, fmt.Errorf("timed out waiting for target. last error: %w", lastErr)
		case <-time.After(200 * time.Millisecond):
			targets, err := e.DiscoverTargets("com.github.steadybit.extension_container.container")
			if err != nil {
				lastErr = err
				continue
			}
			for _, target := range targets {
				if predicate(target) {
					return target, nil
				}
			}
		}
	}
}

func hasAttribute(target discovery_kit_api.Target, key, value string) bool {
	for _, v := range target.Attributes[key] {
		if v == value {
			return true
		}
	}
	return false
}
