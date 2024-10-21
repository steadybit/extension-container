// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package container

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/containerd"
	"github.com/steadybit/extension-container/extcontainer/container/crio"
	"github.com/steadybit/extension-container/extcontainer/container/docker"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/exthealth"
	"os"
	"time"
)

func AutoDetect() (runtime types.Runtime) {
	for _, r := range types.AllRuntimes {
		if _, err := os.Stat(r.DefaultSocket()); err == nil {
			return r
		}
	}
	return ""
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
