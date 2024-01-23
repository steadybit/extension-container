// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import (
	"context"
	"fmt"
	dcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dclient "github.com/docker/docker/client"
	"github.com/steadybit/extension-container/extcontainer"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extutil"
	"strings"
)

type client struct {
	docker *dclient.Client
}

func (c *client) Socket() string {
	return c.docker.DaemonHost()
}

func (c *client) Runtime() types.Runtime {
	return types.RuntimeDocker
}

func New(address string) (types.Client, error) {
	if !strings.Contains(address, "://") {
		address = "unix://" + address
	}
	dockerClient, err := dclient.NewClientWithOpts(dclient.WithHost(address), dclient.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker dclient: %w", err)
	}
	return &client{dockerClient}, nil
}

func (c *client) List(ctx context.Context) ([]types.Container, error) {
	listFilters := filters.NewArgs()
	listFilters.Add("status", "restarting")
	listFilters.Add("status", "running")
	listFilters.Add("status", "paused")

	containers, err := c.docker.ContainerList(ctx, dcontainer.ListOptions{Filters: listFilters})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]types.Container, 0, len(containers))
	for _, container := range containers {
		result = append(result, &Container{container})
	}
	return result, nil
}

func (c *client) GetPid(ctx context.Context, containerId string) (int, error) {
	info, err := c.docker.ContainerInspect(ctx, extcontainer.RemovePrefix(containerId))
	if err != nil {
		return 0, fmt.Errorf("failed to inspect container: %w", err)
	}
	return info.State.Pid, nil
}

func (c *client) Pause(ctx context.Context, id string) error {
	return c.docker.ContainerPause(ctx, id)
}

func (c *client) Unpause(ctx context.Context, id string) error {
	return c.docker.ContainerUnpause(ctx, id)
}

func (c *client) Stop(ctx context.Context, id string, graceful bool) error {
	opt := dcontainer.StopOptions{}
	if !graceful {
		opt.Timeout = extutil.Ptr(0)
	}

	err := c.docker.ContainerStop(ctx, id, opt)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}
	return nil
}

func (c *client) Version(ctx context.Context) (string, error) {
	version, err := c.docker.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

func (c *client) Close() error {
	return c.docker.Close()
}
