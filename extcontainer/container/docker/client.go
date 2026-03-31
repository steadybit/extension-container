// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package docker

import (
	"context"
	"fmt"
	dclient "github.com/moby/moby/client"
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
	listFilters := make(dclient.Filters)
	listFilters.Add("status", "restarting", "running", "paused")

	listResult, err := c.docker.ContainerList(ctx, dclient.ContainerListOptions{Filters: listFilters})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]types.Container, 0, len(listResult.Items))
	for _, container := range listResult.Items {
		result = append(result, newContainer(container))
	}
	return result, nil
}

func (c *client) Info(ctx context.Context, id string) (types.Container, error) {
	r, err := c.docker.ContainerInspect(ctx, id, dclient.ContainerInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get docker container %s: %w", id, err)
	}
	return newContainerFromInspect(r.Container), nil
}

func (c *client) GetPid(ctx context.Context, containerId string) (int, error) {
	info, err := c.docker.ContainerInspect(ctx, extcontainer.RemovePrefix(containerId), dclient.ContainerInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to inspect container: %w", err)
	}
	return info.Container.State.Pid, nil
}

func (c *client) Pause(ctx context.Context, id string) error {
	_, err := c.docker.ContainerPause(ctx, id, dclient.ContainerPauseOptions{})
	return err
}

func (c *client) Unpause(ctx context.Context, id string) error {
	_, err := c.docker.ContainerUnpause(ctx, id, dclient.ContainerUnpauseOptions{})
	return err
}

func (c *client) Stop(ctx context.Context, id string, graceful bool) error {
	opt := dclient.ContainerStopOptions{}
	if !graceful {
		opt.Timeout = extutil.Ptr(0)
	}

	_, err := c.docker.ContainerStop(ctx, id, opt)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}
	return nil
}

func (c *client) Version(ctx context.Context) (string, error) {
	version, err := c.docker.ServerVersion(ctx, dclient.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

func (c *client) Close() error {
	return c.docker.Close()
}
