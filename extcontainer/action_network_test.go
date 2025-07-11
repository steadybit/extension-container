// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extconversion"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"hash/fnv"
	"math/rand/v2"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func Test_should_revert_event_when_namespace_is_missing(t *testing.T) {
	defer func() {
		getProcessInfoForContainer = getProcessInfoForContainerImpl
	}()

	//given a started network action
	action := &networkAction{
		ociRuntime:  newMockedRunc(),
		client:      newMockedContainerClient().addContainer("test-container", nil),
		description: action_kit_api.ActionDescription{},
		optsProvider: func(ctx context.Context, sidecar network.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (network.Opts, action_kit_api.Messages, error) {
			port := uint16(rand.IntN(65535))
			return &network.BlackholeOpts{
				Filter: network.Filter{
					Include: []network.NetWithPortRange{
						{Net: network.NetAnyIpv4, PortRange: network.PortRange{From: port, To: port}},
					},
				},
			}, nil, nil
		},
		optsDecoder: blackholeDecode,
	}

	ctx := context.Background()
	state := &NetworkActionState{}

	target := action_kit_api.Target{Attributes: map[string][]string{"container.id": {"test-container"}}}
	getProcessInfoForContainer = func(ctx context.Context, r ociruntime.OciRuntime, containerId string, nsTypes ...specs.LinuxNamespaceType) (ociruntime.LinuxProcessInfo, error) {
		return ociruntime.LinuxProcessInfo{
			Pid: 123,
			Namespaces: []ociruntime.LinuxNamespace{
				{
					Type:  specs.NetworkNamespace,
					Path:  "/some-path",
					Inode: 9999,
				},
			},
		}, nil
	}

	prepare, err := action.Prepare(ctx, state, action_kit_api.PrepareActionRequestBody{Target: &target})
	require.NoError(t, err)
	extractState(t, &prepare.State, state)

	start, err := action.Start(context.Background(), state)
	require.NoError(t, err)
	extractState(t, start.State, state)

	//when the stop is called for a net namespace that is gone
	_, err = action.Stop(context.Background(), state)
	require.NoError(t, err)

	//then we can start attacks again for the same net namespace (in case it is reused)
	getProcessInfoForContainer = func(ctx context.Context, r ociruntime.OciRuntime, containerId string, nsTypes ...specs.LinuxNamespaceType) (ociruntime.LinuxProcessInfo, error) {
		return ociruntime.LinuxProcessInfo{
			Pid: 456,
			Namespaces: []ociruntime.LinuxNamespace{
				{
					Type:  specs.NetworkNamespace,
					Path:  "/other-path",
					Inode: 9999,
				},
			},
		}, nil
	}

	prepare2, err := action.Prepare(ctx, state, action_kit_api.PrepareActionRequestBody{Target: &target})
	require.NoError(t, err)
	extractState(t, &prepare2.State, state)

	start2, err := action.Start(context.Background(), state)
	require.NoError(t, err)
	extractState(t, start2.State, state)

	_, err = action.Stop(context.Background(), state)
	require.NoError(t, err)
}

func extractState(t *testing.T, res *action_kit_api.ActionState, state *NetworkActionState) {
	require.NoError(t, extconversion.Convert(res, state))
}

func newMockedRunc() *MockedRunc {
	bundle := MockBundle{id: "1", path: "/1"}
	bundle.On("EditSpec", mock.Anything, mock.Anything).Return(nil)
	bundle.On("Remove", mock.Anything, mock.Anything).Return(nil)
	bundle.On("CopyFileFromProcess", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	bundle.On("MountFromProcess", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	runcMock := &MockedRunc{}
	runcMock.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(&bundle, nil)
	runcMock.On("Run", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	runcMock.On("Delete", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	//runcMock.On("State", mock.Anything, mock.Anything).Return(&ociRuntime.ContainerState{
	//	Status: "running",
	//}, nil)
	return runcMock
}

type MockedRunc struct {
	mock.Mock
}

func (m *MockedRunc) State(ctx context.Context, id string) (*ociruntime.ContainerState, error) {
	args := m.Called(ctx, id)

	state := args.Get(0).(*ociruntime.ContainerState)
	if state != nil {
		state.ID = id
		state.Pid = hash(id)
		state.Bundle = fmt.Sprintf("/bundle/%d", state.Pid)
		state.Rootfs = "/"
		state.Created = time.Now()
	}

	return state, args.Error(1)
}

func hash(s string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32())
}

func (m *MockedRunc) Create(ctx context.Context, image, id string) (ociruntime.ContainerBundle, error) {
	args := m.Called(ctx, image, id)
	return args.Get(0).(ociruntime.ContainerBundle), args.Error(1)
}

func (m *MockedRunc) Run(ctx context.Context, container ociruntime.ContainerBundle, ioOpts ociruntime.IoOpts) error {
	args := m.Called(ctx, container, ioOpts)
	return args.Error(0)
}

func (m *MockedRunc) Delete(ctx context.Context, id string, force bool) error {
	args := m.Called(ctx, id, force)
	return args.Error(0)
}

func (m *MockedRunc) RunCommand(_ context.Context, _ ociruntime.ContainerBundle) (*exec.Cmd, error) {
	panic("implement me")
}

func (m *MockedRunc) Kill(_ context.Context, _ string, _ syscall.Signal) error {
	panic("implement me")
}

type MockBundle struct {
	mock.Mock
	path string
	id   string
}

func (m *MockBundle) EditSpec(editors ...ociruntime.SpecEditor) error {
	args := m.Called(editors)
	return args.Error(0)
}

func (m *MockBundle) MountFromProcess(ctx context.Context, fromPid int, fromPath, mountpoint string) error {
	args := m.Called(ctx, fromPid, fromPath, mountpoint)
	return args.Error(0)
}

func (m *MockBundle) CopyFileFromProcess(ctx context.Context, pid int, fromPath, toPath string) error {
	args := m.Called(ctx, pid, fromPath, toPath)
	return args.Error(0)
}

func (m *MockBundle) Path() string {
	return m.path
}

func (m *MockBundle) ContainerId() string {
	return m.id
}

func (m *MockBundle) Remove() error {
	args := m.Called()
	return args.Error(0)
}

func newMockedContainerClient() *MockedClient {
	return &MockedClient{}
}

type MockedClient struct {
	c []mockedContainer
}

func (c *MockedClient) addContainer(id string, labels map[string]string) *MockedClient {
	c.c = append(c.c, mockedContainer{id: id, labels: labels})
	return c
}

func (c *MockedClient) List(_ context.Context) ([]types.Container, error) {
	panic("implement me")
}

func (c *MockedClient) Info(_ context.Context, id string) (types.Container, error) {
	for _, container := range c.c {
		if container.id == id {
			return container, nil
		}
	}
	return nil, fmt.Errorf("container not found")
}

func (c *MockedClient) Stop(_ context.Context, _ string, _ bool) error {
	panic("implement me")
}

func (c *MockedClient) Pause(_ context.Context, _ string) error {
	panic("implement me")
}

func (c *MockedClient) Unpause(_ context.Context, _ string) error {
	panic("implement me")
}

func (c *MockedClient) Version(_ context.Context) (string, error) {
	panic("implement me")
}

func (c *MockedClient) GetPid(_ context.Context, _ string) (int, error) {
	panic("implement me")
}

func (c *MockedClient) Close() error {
	panic("implement me")
}

func (c *MockedClient) Runtime() types.Runtime {
	panic("implement me")
}

func (c *MockedClient) Socket() string {
	panic("implement me")
}

type mockedContainer struct {
	id     string
	labels map[string]string
}

func (m mockedContainer) Id() string {
	return m.id
}

func (m mockedContainer) Name() string {
	return fmt.Sprintf("mocked-%s", m.id)
}

func (m mockedContainer) ImageName() string {
	return "mocked-image-name"
}

func (m mockedContainer) Labels() map[string]string {
	return m.labels
}
