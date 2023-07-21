// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package containerd

import (
	"context"
	"fmt"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/extcontainer"
	"strings"
	"syscall"
	"time"
)

// Client implements the engines.Client interface for containerd
type Client struct {
	containerd *containerd.Client
}

func (c *Client) Socket() string {
	return c.containerd.Conn().Target()
}

func New(socket string, namespace string) (types.Client, error) {
	containerdClient, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}
	return &Client{containerdClient}, nil
}

func (c *Client) Runtime() types.Runtime {
	return types.RuntimeContainerd
}

func (c *Client) List(ctx context.Context) ([]types.Container, error) {
	containers, err := c.containerd.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var result []types.Container
	for _, container := range containers {
		if status, err := getStatus(ctx, container); status.Status != containerd.Running &&
			status.Status != containerd.Paused && status.Status != containerd.Pausing {
			if err != nil && !errdefs.IsNotFound(err) {
				log.Warn().Err(err).Msg("Failed to get status for container")
			}
			continue
		}

		if mapped, err := toContainer(ctx, container); err == nil {
			result = append(result, mapped)
		}
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get info for container")
		}
	}

	return result, nil
}

func getStatus(ctx context.Context, container containerd.Container) (containerd.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	task, err := container.Task(ctx, nil)
	if err != nil {
		return containerd.Status{}, err
	}
	return task.Status(ctx)
}

func (c *Client) GetPid(ctx context.Context, containerId string) (int, error) {
	container, err := c.containerd.LoadContainer(ctx, extcontainer.RemovePrefix(containerId))
	if err != nil {
		return 0, fmt.Errorf("failed to load container: %w", err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to load task for container: %w", err)
	}
	return int(task.Pid()), nil
}

func toContainer(ctx context.Context, container containerd.Container) (*Container, error) {
	info, err := container.Info(ctx)
	if err != nil {
		return nil, err
	}
	result := Container{
		info,
	}
	return &result, nil
}

func (c *Client) Pause(ctx context.Context, id string) error {
	container, err := c.containerd.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no running task found") {
			return fmt.Errorf("couldn't pause container as container %s wasn't running: %w", id, err)
		}
		return fmt.Errorf("failed to load task for container %s: %w", id, err)
	}
	return task.Pause(ctx)
}

func (c *Client) Unpause(ctx context.Context, id string) error {
	container, err := c.containerd.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no running task found") {
			return fmt.Errorf("couldn't unpause container as container %s wasn't running: %w", id, err)
		}
		return fmt.Errorf("failed to load task for container %s: %w", id, err)
	}
	return task.Resume(ctx)
}

func (c *Client) Stop(ctx context.Context, id string, graceful bool) error {
	container, err := c.containerd.LoadContainer(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no running task found") {
			return fmt.Errorf("couldn't stop container as container %s wasn't running: %w", id, err)
		}
		return fmt.Errorf("failed to load task for container %s: %w", id, err)
	}

	if graceful {
		log.Info().Msgf("Sending SIGTERM to container %s", id)
		err = task.Kill(ctx, syscall.SIGTERM)
		if err != nil {
			return fmt.Errorf("failed to stop container %s: %w", id, err)
		}

		waitChannel, err := task.Wait(ctx)
		if err != nil {
			return fmt.Errorf("failed to wait for container stop %s: %w", id, err)
		}

		select {
		case exitStatus := <-waitChannel:
			if exitStatus.Error() != nil {
				return fmt.Errorf("failed to stop container %s during grace period : %w", id, exitStatus.Error())
			}
			log.Info().Str("containerId", id).Msgf("container stopped gracefully.")
			return nil
		case <-time.After(10 * time.Second):
			log.Info().Str("containerId", id).Msgf("container did not stop gracefully.")
		}
	}

	log.Info().Str("containerId", id).Msgf("Sending SIGKILL to container")
	err = task.Kill(ctx, syscall.SIGKILL)
	if err != nil {
		return fmt.Errorf("failed to kill container %s: %w", id, err)
	}

	return nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	version, err := c.containerd.Version(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

func (c *Client) Close() error {
	return c.containerd.Close()
}
