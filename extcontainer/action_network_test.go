// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"net"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/proxyfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/extension-container/config"
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
		optsProvider: func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error) {
			port := uint16(rand.IntN(65535))
			return &netfault.BlackholeOpts{
				Filter: netfault.Filter{
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

func Test_mapToNetworkFilter_excludeIp(t *testing.T) {
	tests := []struct {
		name         string
		actionConfig map[string]any
		wantExcluded []string
	}{
		{
			name:         "no excludeIp yields no parameter excludes",
			actionConfig: map[string]any{},
			wantExcluded: nil,
		},
		{
			name: "excludeIp CIDRs and IPs are excluded on all ports",
			actionConfig: map[string]any{
				"excludeIp": []any{"10.0.0.0/8", "192.168.1.1"},
			},
			wantExcluded: []string{"10.0.0.0/8 # parameters", "192.168.1.1/32 # parameters"},
		},
		{
			name: "excludeIp composes with include restrictions",
			actionConfig: map[string]any{
				"ip":        []any{"10.0.0.0/8"},
				"excludeIp": []any{"10.1.0.0/16"},
			},
			wantExcluded: []string{"10.1.0.0/16 # parameters"},
		},
		{
			name: "excludeHostname entries are excluded together with excludeIp",
			actionConfig: map[string]any{
				"excludeIp":       []any{"10.0.0.0/8"},
				"excludeHostname": []any{"192.168.1.1"},
			},
			wantExcluded: []string{"10.0.0.0/8 # parameters", "192.168.1.1/32 # parameters"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, _, err := mapToNetworkFilter(context.Background(), newMockedRunc(), netfault.SidecarOpts{}, tt.actionConfig, nil)
			require.NoError(t, err)

			var parameterExcludes []string
			for _, e := range filter.Exclude {
				if e.Comment == "parameters" {
					require.Equal(t, network.PortRangeAny, e.PortRange)
					parameterExcludes = append(parameterExcludes, e.String())
				}
			}
			require.Equal(t, tt.wantExcluded, parameterExcludes)

			for _, i := range filter.Include {
				require.Equal(t, "parameters", i.Comment)
			}
		})
	}
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

func Test_dependency_defaultPorts(t *testing.T) {
	require.Equal(t, "80,443", (&dependencyFaultAction{spec: latencyFaultSpec}).defaultPorts())
	require.Equal(t, "80,443", (&dependencyFaultAction{spec: resetFaultSpec}).defaultPorts())
	// HTTP abort is cleartext-only, so 443 is dropped from the default.
	require.Equal(t, "80", (&dependencyFaultAction{spec: httpAbortFaultSpec}).defaultPorts())

	// The Describe()d port parameter default reflects it.
	portDefault := func(a *dependencyFaultAction) string {
		for _, p := range a.Describe().Parameters {
			if p.Name == "port" {
				return *p.DefaultValue
			}
		}
		return ""
	}
	require.Equal(t, "80", portDefault(&dependencyFaultAction{spec: httpAbortFaultSpec}))
	require.Equal(t, "80,443", portDefault(&dependencyFaultAction{spec: latencyFaultSpec}))
}

func Test_dependencyFault_hint(t *testing.T) {
	// The HTTP-abort action carries an action-level (global) hint warning that the
	// synthesized status/body applies to cleartext HTTP only.
	httpAbort := (&dependencyFaultAction{spec: httpAbortFaultSpec}).Describe()
	require.NotNil(t, httpAbort.Hint)
	require.Equal(t, action_kit_api.HintWarning, httpAbort.Hint.Type)
	require.Contains(t, httpAbort.Hint.Content, "cleartext HTTP")

	// The latency action has no such restriction, so it carries no hint.
	require.Nil(t, (&dependencyFaultAction{spec: latencyFaultSpec}).Describe().Hint)
}

func mustCIDR(t *testing.T, s string) net.IPNet {
	_, n, err := net.ParseCIDR(s)
	require.NoError(t, err)
	return *n
}

func Test_scopesOverlap(t *testing.T) {
	all := []net.IPNet{mustCIDR(t, "0.0.0.0/0")}
	ten := []net.IPNet{mustCIDR(t, "10.0.0.0/8")}
	priv := []net.IPNet{mustCIDR(t, "192.168.0.0/16")}

	require.True(t, portsOverlap([]uint16{80, 443}, []uint16{443}))
	require.False(t, portsOverlap([]uint16{80}, []uint16{443}))
	require.True(t, cidrsOverlap(all, ten))
	require.True(t, cidrsOverlap(ten, all))
	require.False(t, cidrsOverlap(ten, priv))

	// Overlap requires BOTH ports and CIDRs to intersect.
	require.True(t, scopesOverlap(proxyfault.Opts{IncludeCIDRs: all, Ports: []uint16{80, 443}}, all, []uint16{443}))
	require.False(t, scopesOverlap(proxyfault.Opts{IncludeCIDRs: all, Ports: []uint16{80}}, all, []uint16{443}))   // ports disjoint
	require.False(t, scopesOverlap(proxyfault.Opts{IncludeCIDRs: ten, Ports: []uint16{443}}, priv, []uint16{443})) // cidrs disjoint
}

func Test_reserveDependencyFaultHandle_conflict(t *testing.T) {
	dependencyFaultHandlesLock.Lock()
	dependencyFaultHandles = map[string]*proxyHandle{}
	dependencyFaultHandlesLock.Unlock()
	t.Cleanup(func() {
		dependencyFaultHandlesLock.Lock()
		dependencyFaultHandles = map[string]*proxyHandle{}
		dependencyFaultHandlesLock.Unlock()
	})

	all := []net.IPNet{mustCIDR(t, "0.0.0.0/0")}
	ns := func(inode uint64) ociruntime.LinuxProcessInfo {
		return ociruntime.LinuxProcessInfo{Namespaces: []ociruntime.LinuxNamespace{{Type: specs.NetworkNamespace, Inode: inode}}}
	}
	optsFor := func(ports ...uint16) proxyfault.Opts {
		return proxyfault.Opts{IncludeCIDRs: all, Ports: ports}
	}
	// Active fault: netns 100, ports 80+443.
	storeDependencyFaultHandle("exec-A", &proxyHandle{proxy: fakeProxy{}, opts: optsFor(80, 443), sidecar: ns(100)})

	// Same netns + overlapping ports => conflict with exec-A.
	reserved, conflict := reserveDependencyFaultHandle("exec-B", ns(100), optsFor(443))
	require.False(t, reserved)
	require.Equal(t, "exec-A", conflict)

	// Same netns, disjoint ports => reserved, no conflict.
	reserved, conflict = reserveDependencyFaultHandle("exec-C", ns(100), optsFor(8080))
	require.True(t, reserved)
	require.Empty(t, conflict)

	// A pending reservation (recorded scope, not yet filled) still conflicts —
	// this is the TOCTOU the recorded-scope reservation closes.
	reserved, conflict = reserveDependencyFaultHandle("exec-C2", ns(100), optsFor(8080))
	require.False(t, reserved)
	require.Equal(t, "exec-C", conflict)
	removeDependencyFaultHandle("exec-C")

	// Different netns => reserved, no conflict.
	reserved, conflict = reserveDependencyFaultHandle("exec-D", ns(200), optsFor(443))
	require.True(t, reserved)
	require.Empty(t, conflict)
	removeDependencyFaultHandle("exec-D")

	// Same execution id => idempotent: not reserved, not a conflict.
	reserved, conflict = reserveDependencyFaultHandle("exec-A", ns(100), optsFor(443))
	require.False(t, reserved)
	require.Empty(t, conflict)
}

// fakeProxy is a filled (non-reservation) handle marker for the conflict test.
type fakeProxy struct{}

func (fakeProxy) Start() error                         { return nil }
func (fakeProxy) Stop() error                          { return nil }
func (fakeProxy) Exited() (bool, error)                { return false, nil }
func (fakeProxy) Metrics() (proxyfault.Snapshot, bool) { return proxyfault.Snapshot{}, false }

func Test_percentageToProbability(t *testing.T) {
	// 0% must map to never (0.0), not always — the whole point of the fix.
	require.Equal(t, 0.0, percentageToProbability(0))
	require.Equal(t, 0.0, percentageToProbability(int64(0)))
	require.Equal(t, 1.0, percentageToProbability(100))
	require.Equal(t, 0.5, percentageToProbability(50))
	require.Equal(t, 0.25, percentageToProbability(25.0))
	// Numeric strings parse (so an explicit "0" still means never).
	require.Equal(t, 0.0, percentageToProbability("0"))
	require.Equal(t, 0.5, percentageToProbability("50"))
	// Out-of-range clamps; unparseable falls back to the 50% default (not "always").
	require.Equal(t, 1.0, percentageToProbability(150))
	require.Equal(t, 0.0, percentageToProbability(-10))
	require.Equal(t, 0.5, percentageToProbability("nope"))
}

func Test_dependency_hostname_required_and_first(t *testing.T) {
	for _, spec := range []dependencyFaultSpec{latencyFaultSpec, httpAbortFaultSpec, resetFaultSpec} {
		var hostname *action_kit_api.ActionParameter
		for i := range (&dependencyFaultAction{spec: spec}).Describe().Parameters {
			p := (&dependencyFaultAction{spec: spec}).Describe().Parameters[i]
			if p.Name == "hostname" {
				hostname = &p
			}
		}
		require.NotNil(t, hostname, "spec %s missing hostname param", spec.id)
		require.True(t, *hostname.Required, "hostname must be required for %s", spec.id)
		require.Equal(t, 0, *hostname.Order, "hostname must be first (order 0) for %s", spec.id)
	}
}

func Test_httpAbort_Prepare_rejects_https_port(t *testing.T) {
	a := &dependencyFaultAction{spec: httpAbortFaultSpec}

	// 443 present -> user-facing error before any container lookup (so a nil
	// runtime/client is never reached). httpStatus is required and always sent by
	// the platform, so include it.
	for _, ports := range []string{"443", "80,443"} {
		res, err := a.Prepare(context.Background(), &DependencyFaultState{}, action_kit_api.PrepareActionRequestBody{
			Config: map[string]interface{}{"port": ports, "httpStatus": 503, "percentage": 100},
		})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Error, "ports %q should be rejected", ports)
		require.Equal(t, action_kit_api.Failed, *res.Error.Status)
	}
}

// withInterceptCA points the global config at a CA for the duration of a test.
func withInterceptCA(t *testing.T) {
	t.Helper()
	prevCert, prevKey := config.Config.TLSInterceptCaCert, config.Config.TLSInterceptCaKey
	config.Config.TLSInterceptCaCert = "/etc/steadybit/ca.crt"
	config.Config.TLSInterceptCaKey = "/etc/steadybit/ca.key"
	t.Cleanup(func() {
		config.Config.TLSInterceptCaCert, config.Config.TLSInterceptCaKey = prevCert, prevKey
	})
}

func Test_dependencyFault_tlsInterceptCA(t *testing.T) {
	httpAbort := &dependencyFaultAction{spec: httpAbortFaultSpec}
	latency := &dependencyFaultAction{spec: latencyFaultSpec}

	// Unconfigured: HTTPS is never decrypted.
	require.Nil(t, httpAbort.tlsInterceptCA())
	require.Equal(t, "80", httpAbort.defaultPorts())

	withInterceptCA(t)

	ca := httpAbort.tlsInterceptCA()
	require.NotNil(t, ca)
	require.Equal(t, "/etc/steadybit/ca.crt", ca.CertPath)
	require.Equal(t, "/etc/steadybit/ca.key", ca.KeyPath)
	// 443 becomes a sensible default once the proxy can terminate it.
	require.Equal(t, "80,443", httpAbort.defaultPorts())

	// Latency already works over HTTPS at L4, so it never asks to decrypt.
	require.Nil(t, latency.tlsInterceptCA())
	require.Equal(t, "80,443", latency.defaultPorts())
}

func Test_dependencyFault_hint_withInterceptCA(t *testing.T) {
	withInterceptCA(t)

	// The cleartext-only warning would now be wrong, so it is replaced by the
	// constraint that actually applies: the target must trust the CA.
	hint := (&dependencyFaultAction{spec: httpAbortFaultSpec}).hint()
	require.NotNil(t, hint)
	require.Equal(t, action_kit_api.HintInfo, hint.Type)
	require.Contains(t, hint.Content, "trust the configured CA")
	require.NotContains(t, hint.Content, "cleartext HTTP only")

	require.Nil(t, (&dependencyFaultAction{spec: latencyFaultSpec}).hint())
}

func Test_formatDependencyStatsMarkdown_rejected(t *testing.T) {
	// A rejection is the "CA isn't trusted" diagnosis; without it the operator
	// only sees connections that matched but were never faulted.
	md := formatDependencyStatsMarkdown(proxyfault.Snapshot{
		ConnectionsMatched:   2,
		ConnectionsFaulted:   0,
		TLSInterceptRejected: 2,
	})
	require.Contains(t, md, "rejected the injected certificate")
	require.Contains(t, md, "trust the configured CA")

	// Nothing rejected: no scary note.
	md = formatDependencyStatsMarkdown(proxyfault.Snapshot{ConnectionsMatched: 1, ConnectionsFaulted: 1})
	require.NotContains(t, md, "rejected the injected certificate")
}
