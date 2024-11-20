// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
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
		runc:        newMockedRunc(),
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
	getProcessInfoForContainer = func(ctx context.Context, r runc.Runc, containerId string) (runc.LinuxProcessInfo, error) {
		return runc.LinuxProcessInfo{
			Pid: 123,
			Namespaces: []runc.LinuxNamespace{
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
	getProcessInfoForContainer = func(ctx context.Context, r runc.Runc, containerId string) (runc.LinuxProcessInfo, error) {
		return runc.LinuxProcessInfo{
			Pid: 456,
			Namespaces: []runc.LinuxNamespace{
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
	//runcMock.On("State", mock.Anything, mock.Anything).Return(&runc.ContainerState{
	//	Status: "running",
	//}, nil)
	return runcMock
}

type MockedRunc struct {
	mock.Mock
}

func (m *MockedRunc) State(ctx context.Context, id string) (*runc.ContainerState, error) {
	args := m.Called(ctx, id)

	state := args.Get(0).(*runc.ContainerState)
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

func (m *MockedRunc) Create(ctx context.Context, image, id string) (runc.ContainerBundle, error) {
	args := m.Called(ctx, image, id)
	return args.Get(0).(runc.ContainerBundle), args.Error(1)
}

func (m *MockedRunc) Run(ctx context.Context, container runc.ContainerBundle, ioOpts runc.IoOpts) error {
	args := m.Called(ctx, container, ioOpts)
	return args.Error(0)
}

func (m *MockedRunc) Delete(ctx context.Context, id string, force bool) error {
	args := m.Called(ctx, id, force)
	return args.Error(0)
}

func (m *MockedRunc) RunCommand(_ context.Context, _ runc.ContainerBundle) (*exec.Cmd, error) {
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

func (m *MockBundle) EditSpec(editors ...runc.SpecEditor) error {
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
