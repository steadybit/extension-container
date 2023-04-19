// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import (
	"context"
	"fmt"
	dtypes "github.com/docker/docker/api/types"
	dcontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/extcontainer"
	"github.com/steadybit/extension-kit/extutil"
	"strings"
)

// Client implements the types.Client interface for Docker
type Client struct {
	docker *client.Client
}

func (c *Client) Socket() string {
	return c.docker.DaemonHost()
}

func (c *Client) Runtime() types.Runtime {
	return types.RuntimeDocker
}

func New(address string) (types.Client, error) {
	if !strings.Contains(address, "://") {
		address = "unix://" + address
	}
	dockerClient, err := client.NewClientWithOpts(client.WithHost(address), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	return &Client{dockerClient}, nil
}

func (c *Client) List(ctx context.Context) ([]types.Container, error) {
	containers, err := c.docker.ContainerList(ctx, dtypes.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]types.Container, len(containers))
	for i, container := range containers {
		result[i] = &Container{container}
	}

	return result, nil
}

func (c *Client) GetPid(ctx context.Context, containerId string) (int, error) {
	info, err := c.docker.ContainerInspect(ctx, extcontainer.RemovePrefix(containerId))
	if err != nil {
		return 0, fmt.Errorf("failed to inspect container: %w", err)
	}
	return info.State.Pid, nil
}

func (c *Client) Pause(ctx context.Context, id string) error {
	return c.docker.ContainerPause(ctx, id)
}

func (c *Client) Unpause(ctx context.Context, id string) error {
	return c.docker.ContainerUnpause(ctx, id)
}

func (c *Client) Stop(ctx context.Context, id string, graceful bool) error {
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

func (c *Client) Version(ctx context.Context) (string, error) {
	version, err := c.docker.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

func (c *Client) Close() error {
	return c.Close()
}
