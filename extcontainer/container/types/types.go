// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package types

import (
	"context"
)

type Container interface {
	Id() string
	Name() string
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
	RuntimePodman             Runtime = "podman"
	DefaultSocketPodman               = "/run/podman/podman.sock"
	// DefaultRuncRootPodman is only a fallback: the podman client reports the OCI runtime and its root it actually uses.
	DefaultRuncRootPodman = "/run/runc"
)

var (
	AllRuntimes = []Runtime{RuntimeDocker, RuntimeContainerd, RuntimeCrio, RuntimePodman}
)

type Runtime string

// ContainerState describes the coarse grained lifecycle state of a container.
type ContainerState string

const (
	// StateRunning means the container has a running task which isn't paused.
	StateRunning ContainerState = "running"
	// StatePaused means the container has a task which is paused (or about to be paused).
	StatePaused ContainerState = "paused"
	// StateStopped means the container has no running task anymore - it may be exited, removed or never started.
	StateStopped ContainerState = "stopped"
)

type Client interface {
	// List returns a list of all running containers
	List(ctx context.Context) ([]Container, error)
	// Info returns the info of the given container
	Info(ctx context.Context, id string) (Container, error)
	Stop(ctx context.Context, id string, graceful bool) error
	// Pause pauses the given container
	Pause(ctx context.Context, id string) error
	// Unpause unpauses the given container
	Unpause(ctx context.Context, id string) error
	// Version returns the version of the runtime
	Version(ctx context.Context) (string, error)
	// GetPid returns the pid of the given container. It returns an error if the container isn't running anymore.
	GetPid(ctx context.Context, id string) (int, error)
	// State returns the current state of the given container. Containers which are unknown to the runtime are reported as StateStopped.
	State(ctx context.Context, id string) (ContainerState, error)
	// Close closes the client
	Close() error
	// Runtime returns the runtime
	Runtime() Runtime
	// Socket returns the socket
	Socket() string
}

// OciRuntimeInfo describes the OCI runtime a container engine uses to run its containers.
type OciRuntimeInfo struct {
	// Path is the OCI runtime binary, e.g. /usr/bin/crun
	Path string
	// Root is the state directory of the OCI runtime, e.g. /run/crun
	Root string
}

// OciRuntimeInfoProvider is implemented by clients which can tell which OCI runtime the engine uses. The extension
// needs it to look up the target containers with the very same OCI runtime and root.
type OciRuntimeInfoProvider interface {
	OciRuntimeInfo(ctx context.Context) (OciRuntimeInfo, error)
}

func (runtime Runtime) DefaultSocket() string {
	switch runtime {
	case RuntimeDocker:
		return DefaultSocketDocker
	case RuntimeContainerd:
		return DefaultSocketContainerd
	case RuntimeCrio:
		return DefaultSocketCrio
	case RuntimePodman:
		return DefaultSocketPodman
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
	case RuntimePodman:
		return DefaultRuncRootPodman
	}
	return ""
}
