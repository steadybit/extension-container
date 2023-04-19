// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package types

import (
	"context"
)

type Container interface {
	Id() string
	Names() []string
	ImageName() string
	Labels() map[string]string
}

const (
	RuntimeContainerd         Runtime = "containerd"
	DefaultSocketContainerd           = "/run/containerd/containerd.sock"
	DefaultRuncRootContainerd         = "/run/containerd/runc/k8s.io"
	RuntimeDocker             Runtime = "docker"
	DefaultSocketDocker               = "/var/run/docker.sock"
	DefaultRuncRootDocker             = "/run/docker/runtime-runc/moby"
	RuntimeCrio               Runtime = "cri-o"
	DefaultSocketCrio                 = "/var/run/crio/crio.sock"
	DefaultRuncRootCrio               = "/run/runc"
)

var (
	AllRuntimes = []Runtime{RuntimeDocker, RuntimeContainerd, RuntimeCrio}
)

type Runtime string

type Client interface {
	List(ctx context.Context) ([]Container, error)
	Stop(ctx context.Context, id string, graceful bool) error
	Pause(ctx context.Context, id string) error
	Unpause(ctx context.Context, id string) error
	Version(ctx context.Context) (string, error)
	GetPid(ctx context.Context, id string) (int, error)
	Close() error
	Runtime() Runtime
	Socket() string
}

func (runtime Runtime) DefaultSocket() string {
	switch runtime {
	case RuntimeDocker:
		return DefaultSocketDocker
	case RuntimeContainerd:
		return DefaultSocketContainerd
	case RuntimeCrio:
		return DefaultSocketCrio
	}
	return ""
}

func (runtime Runtime) DefaultRuncRoot() string {
	switch runtime {
	case RuntimeDocker:
		return DefaultRuncRootDocker
	case RuntimeContainerd:
		return DefaultRuncRootContainerd
	case RuntimeCrio:
		return DefaultRuncRootCrio
	}
	return ""
}
