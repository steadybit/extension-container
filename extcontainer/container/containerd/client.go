// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package containerd

import (
	"context"
	"errors"
	"fmt"
	"github.com/containerd/containerd"
	containersapi "github.com/containerd/containerd/api/services/containers/v1"
	tasksapi "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"io"
	"strings"
	"syscall"
	"time"
)

type client struct {
	containerd *containerd.Client
}

func (c *client) Socket() string {
	return c.containerd.Conn().Target()
}

func New(socket string, namespace string) (types.Client, error) {
	containerdClient, err := containerd.New(socket, containerd.WithDefaultNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}
	return &client{containerdClient}, nil
}

func (c *client) Runtime() types.Runtime {
	return types.RuntimeContainerd
}

var errStreamNotAvailable = errors.New("streaming api not available")

func (c *client) List(ctx context.Context) ([]types.Container, error) {
	containers := containersapi.NewContainersClient(c.containerd.Conn())
	session, err := containers.ListStream(ctx, &containersapi.ListContainersRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", errgrpc.ToNative(err))
	}

	tasks := tasksapi.NewTasksClient(c.containerd.Conn())
	var result []types.Container

	for {
		r, err := session.Recv()
		if err != nil {
			if err == io.EOF {
				return result, nil
			}
			if s, ok := grpcstatus.FromError(err); ok {
				if s.Code() == codes.Unimplemented {
					return nil, errStreamNotAvailable
				}
			}
			return nil, errgrpc.ToNative(err)
		}

		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
			if isContainerAlive(ctx, tasks, r.Container.ID) {
				result = append(result, newContainer(r.Container))
			}
		}
	}
}

func (c *client) Info(ctx context.Context, id string) (types.Container, error) {
	containers := containersapi.NewContainersClient(c.containerd.Conn())
	r, err := containers.Get(ctx, &containersapi.GetContainerRequest{ID: id})
	if err != nil {
		return nil, fmt.Errorf("failed to get container %s: %w", id, errgrpc.ToNative(err))
	}
	return newContainer(r.Container), nil
}

func isContainerAlive(ctx context.Context, tasks tasksapi.TasksClient, id string) bool {
	status, err := getStatus(ctx, tasks, id)
	if err != nil && !errdefs.IsNotFound(err) {
		log.Warn().Err(err).Msg("Failed to get status for container")
		return false
	}
	return status == containerd.Running || status == containerd.Paused || status == containerd.Pausing
}

func getStatus(ctx context.Context, tasks tasksapi.TasksClient, id string) (containerd.ProcessStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	r, err := tasks.Get(ctx, &tasksapi.GetRequest{ContainerID: id})
	if err != nil {
		err = errgrpc.ToNative(err)
		if errdefs.IsNotFound(err) {
			return containerd.Unknown, fmt.Errorf("no running task found: %w", err)
		}
		return containerd.Unknown, err
	}

	return containerd.ProcessStatus(strings.ToLower(r.Process.Status.String())), err
}

func (c *client) GetPid(ctx context.Context, containerId string) (int, error) {
	container, err := c.containerd.LoadContainer(ctx, containerId)
	if err != nil {
		return 0, fmt.Errorf("failed to load container: %w", err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to load task for container: %w", err)
	}
	return int(task.Pid()), nil
}

func (c *client) Pause(ctx context.Context, id string) error {
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

func (c *client) Unpause(ctx context.Context, id string) error {
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

func (c *client) Stop(ctx context.Context, id string, graceful bool) error {
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

func (c *client) Version(ctx context.Context) (string, error) {
	version, err := c.containerd.Version(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

func (c *client) Close() error {
	return c.containerd.Close()
}
