// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package container

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/containerd"
	"github.com/steadybit/extension-container/extcontainer/container/crio"
	"github.com/steadybit/extension-container/extcontainer/container/docker"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/exthealth"
	"os"
	"path/filepath"
	"time"
)

func AutoDetect() (runtime types.Runtime) {
	return autoDetect(func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}, filepath.EvalSymlinks)
}

func autoDetect(exists func(path string) bool, resolve func(path string) (string, error)) types.Runtime {
	for _, r := range types.AllRuntimes {
		socket := r.DefaultSocket()
		if !exists(socket) {
			continue
		}
		// podman-docker links the docker socket to the podman socket; the engine behind it is podman.
		if r == types.RuntimeDocker && isPodmanSocket(socket, resolve) {
			return types.RuntimePodman
		}
		return r
	}
	return ""
}

func isPodmanSocket(socket string, resolve func(path string) (string, error)) bool {
	resolved, err := resolve(socket)
	if err != nil {
		return false
	}
	podmanSocket, err := resolve(types.DefaultSocketPodman)
	if err != nil {
		podmanSocket = types.DefaultSocketPodman
	}
	return resolved == podmanSocket
}

func NewClient() (types.Client, error) {
	runtime := types.Runtime(config.Config.ContainerRuntime)
	socket := config.Config.ContainerSocket

	if runtime == "" {
		runtime = AutoDetect()
	}

	if runtime == "" {
		return nil, fmt.Errorf("failed to detect container runtime, please specify")
	}

	if socket == "" {
		socket = runtime.DefaultSocket()
	}

	switch runtime {
	case types.RuntimeDocker:
		return docker.New(socket)
	case types.RuntimeContainerd:
		return containerd.New(socket, config.Config.ContainerdNamespace)
	case types.RuntimeCrio:
		return crio.New(socket)
	case types.RuntimePodman:
		return docker.NewPodman(socket)
	default:
		return nil, fmt.Errorf("unsupported container runtime: %s", runtime)
	}
}

func RegisterLivenessCheck(client types.Client) chan struct{} {
	if config.Config.LivenessCheckInterval == "" || config.Config.LivenessCheckInterval == "0" {
		log.Info().Msg("Liveness check is disabled.")
		return nil
	}

	duration, err := time.ParseDuration(config.Config.LivenessCheckInterval)
	if err != nil {
		duration = 30 * time.Second
		log.Error().Err(err).Msgf("Failed to parse liveness check interval, using default: %s", duration)
	}
	ticker := time.NewTicker(duration)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				_, err := client.Version(context.Background())
				exthealth.SetAlive(err == nil)
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
	return quit
}

// ResolveOciRuntimeConfig completes the OCI runtime configuration with the runtime the container engine uses, so the
// extension looks up target containers with the same binary and state directory. Explicit configuration always wins.
func ResolveOciRuntimeConfig(ctx context.Context, cfg ociruntime.Config, client types.Client) ociruntime.Config {
	if p, ok := client.(types.OciRuntimeInfoProvider); ok {
		info, err := p.OciRuntimeInfo(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to detect the OCI runtime of the container engine, using the configured defaults.")
		} else {
			if !ociRuntimeEnvSet("PATH") && info.Path != "" {
				cfg.Path = info.Path
			}
			if cfg.Root == "" {
				cfg.Root = info.Root
			}
		}
	}

	if cfg.Root == "" {
		cfg.Root = client.Runtime().DefaultRuncRoot()
	}
	return cfg
}

func ociRuntimeEnvSet(name string) bool {
	for _, prefix := range []string{"STEADYBIT_EXTENSION_OCIRUNTIME_", "STEADYBIT_EXTENSION_RUNC_"} {
		if _, ok := os.LookupEnv(prefix + name); ok {
			return true
		}
	}
	return false
}
