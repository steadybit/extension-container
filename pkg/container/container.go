// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package container

import (
	"fmt"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/pkg/container/containerd"
	"github.com/steadybit/extension-container/pkg/container/crio"
	"github.com/steadybit/extension-container/pkg/container/docker"
	"github.com/steadybit/extension-container/pkg/container/types"
	"os"
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
