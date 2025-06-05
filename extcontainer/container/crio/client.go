// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package crio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	criapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"net"
	"time"
)

type client struct {
	cri        criapi.RuntimeServiceClient
	connection *grpc.ClientConn
}

func (c *client) Socket() string {
	return c.connection.Target()
}

func (c *client) Runtime() types.Runtime {
	return types.RuntimeCrio
}

func New(socket string) (types.Client, error) {
	connection, err := newConnection(socket)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to cri socket: %w", err)
	}
	criClient := criapi.NewRuntimeServiceClient(connection)
	return &client{criClient, connection}, nil
}

func newConnection(socket string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		fmt.Sprintf("passthrough:///%s", socket),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn.Connect()
	if conn.WaitForStateChange(ctx, connectivity.Ready) {
		return conn, nil
	} else {
		return nil, fmt.Errorf("connection failed: %s", conn.GetState())
	}
}

func dialer(ctx context.Context, addr string) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		return net.DialTimeout("unix", addr, time.Until(deadline))
	}
	return net.DialTimeout("unix", addr, 0)
}

func (c *client) List(ctx context.Context) ([]types.Container, error) {
	containerList, err := c.cri.ListContainers(ctx, &criapi.ListContainersRequest{
		Filter: &criapi.ContainerFilter{
			State: &criapi.ContainerStateValue{
				State: criapi.ContainerState_CONTAINER_RUNNING,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list CRI-O containers: %w", err)
	}

	result := make([]types.Container, 0, len(containerList.Containers))
	for _, container := range containerList.Containers {
		result = append(result, newContainer(container))
	}
	return result, nil
}

func (c *client) Info(ctx context.Context, id string) (types.Container, error) {
	r, err := c.cri.ContainerStatus(ctx, &criapi.ContainerStatusRequest{ContainerId: id})
	if err != nil {
		return nil, fmt.Errorf("failed to get CRI-O container %s: %w", id, err)
	}
	return newContainerFromStatus(r.Status), nil
}

func (c *client) GetPid(ctx context.Context, containerId string) (int, error) {
	res, err := c.cri.ContainerStatus(ctx, &criapi.ContainerStatusRequest{
		ContainerId: containerId,
		Verbose:     true,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get container status: %w", err)
	}

	var info struct {
		Pid int `json:"pid"`
	}
	err = json.Unmarshal([]byte(res.GetInfo()["info"]), &info)
	if err != nil {
		return 0, fmt.Errorf("failed to read pid form container verbose info: %w", err)
	}
	if info.Pid == 0 {
		return 0, errors.New("failed to read pid form container verbose info")
	}
	return info.Pid, nil
}

func (c *client) Pause(_ context.Context, _ string) error {
	return fmt.Errorf("not supported")
}

func (c *client) Unpause(_ context.Context, _ string) error {
	return fmt.Errorf("not supported")
}

func (c *client) Stop(ctx context.Context, id string, graceful bool) error {
	timeout := 10
	if !graceful {
		timeout = 0
	}
	_, err := c.cri.StopContainer(ctx, &criapi.StopContainerRequest{
		ContainerId: id,
		Timeout:     int64(timeout),
	})
	if err != nil {
		return fmt.Errorf("failed to stop CRI-O container %s: %w", id, err)
	}
	return nil
}

func (c *client) Version(ctx context.Context) (string, error) {
	versionResponse, err := c.cri.Version(ctx, &criapi.VersionRequest{})
	if err != nil {
		return "", err
	}
	return versionResponse.RuntimeVersion, nil
}

func (c *client) Close() error {
	return c.connection.Close()
}
