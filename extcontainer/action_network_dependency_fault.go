// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/nsrunner"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/proxyfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

// dependencyFaultSpec describes one proxy-based dependency-fault action. The
// three actions (delay / HTTP abort / connection reset) share all interception
// and lifecycle logic and differ only by this spec — mirroring the per-fault
// action split of the istio extension.
type dependencyFaultSpec struct {
	id          string
	label       string
	description string
	extraParams []action_kit_api.ActionParameter
	buildFault  func(cfg map[string]any) (proxyfault.Fault, error)
	// cleartextHTTPOnly marks a fault that can only act on cleartext HTTP (the
	// L7 status injection). It drops 443 from the default intercept ports and
	// makes Prepare reject an explicit 443, since HTTPS/TLS cannot be aborted
	// without terminating TLS.
	cleartextHTTPOnly bool
}

var latencyFaultSpec = dependencyFaultSpec{
	id:          "network_dependency_latency",
	label:       "Delay Dependency Traffic",
	description: "Transparently proxy the container's outgoing traffic to a dependency and add latency, selected by hostname/SNI.",
	extraParams: []action_kit_api.ActionParameter{{
		Name: "delay", Label: "Latency", Description: extutil.Ptr("Latency to add before forwarding to the dependency."),
		Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("500ms"), Required: extutil.Ptr(true), Order: extutil.Ptr(2),
	}},
	buildFault: func(cfg map[string]any) (proxyfault.Fault, error) {
		d := time.Duration(extutil.ToInt64(cfg["delay"])) * time.Millisecond
		if d <= 0 {
			return proxyfault.Fault{}, fmt.Errorf("delay must be greater than 0")
		}
		return proxyfault.Fault{Latency: d}, nil
	},
}

var httpAbortFaultSpec = dependencyFaultSpec{
	id:          "network_dependency_http_abort",
	label:       "HTTP Abort Dependency Traffic",
	description: "Transparently proxy the container's outgoing cleartext HTTP traffic to a dependency and return an HTTP error status, selected by Host header.",
	extraParams: []action_kit_api.ActionParameter{{
		Name: "httpStatus", Label: "HTTP Status", Description: extutil.Ptr("HTTP status code to return (cleartext HTTP only)."),
		Type: action_kit_api.ActionParameterTypeInteger, DefaultValue: extutil.Ptr("503"), Required: extutil.Ptr(true), Order: extutil.Ptr(2),
	}},
	buildFault: func(cfg map[string]any) (proxyfault.Fault, error) {
		status := int(extutil.ToInt64(cfg["httpStatus"]))
		if status < 100 || status > 599 {
			return proxyfault.Fault{}, fmt.Errorf("httpStatus must be within [100,599], got %d", status)
		}
		return proxyfault.Fault{HTTPStatus: status}, nil
	},
	cleartextHTTPOnly: true,
}

var resetFaultSpec = dependencyFaultSpec{
	id:          "network_dependency_reset",
	label:       "Reset Dependency Connections",
	description: "Transparently proxy the container's outgoing traffic to a dependency and reset (RST) matching connections, selected by hostname/SNI.",
	buildFault: func(map[string]any) (proxyfault.Fault, error) {
		return proxyfault.Fault{Reset: true}, nil
	},
}

var _ action_kit_sdk.Action[DependencyFaultState] = (*dependencyFaultAction)(nil)
var _ action_kit_sdk.ActionWithStatus[DependencyFaultState] = (*dependencyFaultAction)(nil)
var _ action_kit_sdk.ActionWithStop[DependencyFaultState] = (*dependencyFaultAction)(nil)

type proxyHandle struct {
	proxy   proxyfault.Proxy
	opts    proxyfault.Opts
	sidecar ociruntime.LinuxProcessInfo
	id      string
}

var (
	dependencyFaultHandles     = map[string]*proxyHandle{}
	dependencyFaultHandlesLock sync.Mutex
)

type DependencyFaultState struct {
	ExecutionId string
	ContainerID string
}

type dependencyFaultAction struct {
	ociRuntime ociruntime.OciRuntime
	client     types.Client
	spec       dependencyFaultSpec
}

func NewNetworkDelayDependencyAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[DependencyFaultState] {
	return &dependencyFaultAction{ociRuntime: r, client: client, spec: latencyFaultSpec}
}

func NewNetworkHttpAbortDependencyAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[DependencyFaultState] {
	return &dependencyFaultAction{ociRuntime: r, client: client, spec: httpAbortFaultSpec}
}

func NewNetworkResetDependencyAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[DependencyFaultState] {
	return &dependencyFaultAction{ociRuntime: r, client: client, spec: resetFaultSpec}
}

func (a *dependencyFaultAction) NewEmptyState() DependencyFaultState {
	return DependencyFaultState{}
}

func (a *dependencyFaultAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.%s", BaseActionID, a.spec.id),
		Label:       a.spec.label,
		Description: a.spec.description,
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Parameters: append(commonDependencyParameters(a.defaultPorts()), a.spec.extraParams...),
	}
}

// defaultPorts is the default intercept-port list shown in the UI. Cleartext-only
// faults (HTTP abort) drop 443, since HTTPS/TLS cannot be aborted.
func (a *dependencyFaultAction) defaultPorts() string {
	if a.spec.cleartextHTTPOnly {
		return "80"
	}
	return "80,443"
}

func (a *dependencyFaultAction) Prepare(ctx context.Context, state *DependencyFaultState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	// Cleartext-only faults (HTTP abort) cannot act on HTTPS: the proxy would have
	// to terminate TLS to inject a status, which it deliberately does not do. Fail
	// early with a clear message rather than silently passing 443 traffic through.
	if a.spec.cleartextHTTPOnly {
		ports, err := parsePortsList(extutil.ToString(request.Config["port"]))
		if err != nil {
			return nil, err
		}
		if slices.Contains(ports, uint16(443)) {
			return &action_kit_api.PrepareResult{Error: &action_kit_api.ActionKitError{
				Title:  "HTTP Abort works on cleartext HTTP only — port 443 (HTTPS/TLS) cannot be aborted without terminating TLS. Remove 443 from the ports, or use 'Reset Dependency Connections' or 'Delay Dependency Traffic' for HTTPS dependencies.",
				Status: extutil.Ptr(action_kit_api.Failed),
			}}, nil
		}
	}

	container, _, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, err
	}
	containerId := RemovePrefix(container.Id())

	processInfo, err := getProcessInfoForContainer(ctx, a.ociRuntime, containerId, specs.NetworkNamespace)
	if err != nil {
		return nil, err
	}

	if isUsingHostNetwork(processInfo.Namespaces) {
		if config.Config.DisallowHostNetwork {
			return &action_kit_api.PrepareResult{Error: &action_kit_api.ActionKitError{
				Title:  "Container is using host network. This is disallowed by your system administrators.",
				Status: extutil.Ptr(action_kit_api.Failed),
			}}, nil
		}
		if extutil.ToBool(request.Config["failOnHostNetwork"]) {
			return &action_kit_api.PrepareResult{Error: &action_kit_api.ActionKitError{
				Title:  "Container is using host network and failOnHostNetwork = true.",
				Status: extutil.Ptr(action_kit_api.Failed),
			}}, nil
		}
	}

	// Idempotent: a repeated Prepare for the same execution reuses the existing
	// proxy rather than creating (and leaking) a second runc sidecar bundle.
	if _, exists := getDependencyFaultHandle(request.ExecutionId.String()); exists {
		state.ExecutionId = request.ExecutionId.String()
		state.ContainerID = containerId
		return &action_kit_api.PrepareResult{}, nil
	}

	opts, err := a.parseOpts(request)
	if err != nil {
		return nil, err
	}

	sidecarId := fmt.Sprintf("%s-%s", request.ExecutionId.String()[24:], containerId[:min(8, len(containerId))])
	proxy, err := proxyfault.NewProcess(ctx, a.ociRuntime, processInfo, sidecarId, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create transparent-proxy process: %w", err)
	}

	state.ExecutionId = request.ExecutionId.String()
	state.ContainerID = containerId
	dependencyFaultHandlesLock.Lock()
	dependencyFaultHandles[state.ExecutionId] = &proxyHandle{proxy: proxy, opts: opts, sidecar: processInfo, id: sidecarId}
	dependencyFaultHandlesLock.Unlock()

	return &action_kit_api.PrepareResult{}, nil
}

func (a *dependencyFaultAction) Start(_ context.Context, state *DependencyFaultState) (*action_kit_api.StartResult, error) {
	handle, ok := getDependencyFaultHandle(state.ExecutionId)
	if !ok {
		return nil, fmt.Errorf("no transparent-proxy handle for execution %s", state.ExecutionId)
	}
	if err := handle.proxy.Start(); err != nil {
		return nil, fmt.Errorf("failed to start transparent-proxy: %w", err)
	}
	return &action_kit_api.StartResult{}, nil
}

func (a *dependencyFaultAction) Status(_ context.Context, state *DependencyFaultState) (*action_kit_api.StatusResult, error) {
	handle, ok := getDependencyFaultHandle(state.ExecutionId)
	if !ok {
		return &action_kit_api.StatusResult{Completed: true}, nil
	}
	if exited, err := handle.proxy.Exited(); exited {
		removeDependencyFaultHandle(state.ExecutionId)
		if err != nil {
			return &action_kit_api.StatusResult{
				Completed: true,
				Error:     &action_kit_api.ActionKitError{Title: fmt.Sprintf("transparent-proxy failed: %v", err), Status: extutil.Ptr(action_kit_api.Errored)},
			}, nil
		}
		// Clean exit (e.g. the --max-duration deadman firing) is a normal
		// completion, not an error.
		return &action_kit_api.StatusResult{Completed: true}, nil
	}
	return &action_kit_api.StatusResult{Completed: false}, nil
}

func (a *dependencyFaultAction) Stop(ctx context.Context, state *DependencyFaultState) (*action_kit_api.StopResult, error) {
	handle, ok := getDependencyFaultHandle(state.ExecutionId)
	if !ok {
		return nil, nil
	}
	removeDependencyFaultHandle(state.ExecutionId)

	if err := handle.proxy.Stop(); err != nil {
		log.Warn().Err(err).Str("execution_id", state.ExecutionId).Msg("failed to stop transparent-proxy")
	}

	runner := nsrunner.NewRuncRunner(a.ociRuntime, nsrunner.SidecarOpts{
		TargetProcess: handle.sidecar,
		Id:            handle.id,
		Env:           []string{"XTABLES_LOCKFILE=/tmp/xtables.lock"},
	})
	if err := proxyfault.Revert(ctx, runner, handle.opts); err != nil {
		log.Warn().Err(err).Str("execution_id", state.ExecutionId).Msg("out-of-band interception revert failed")
	}

	return nil, nil
}

// --- parameters & parsing ---

func commonDependencyParameters(portsDefault string) []action_kit_api.ActionParameter {
	return []action_kit_api.ActionParameter{
		{
			Name: "duration", Label: "Duration", Description: extutil.Ptr("How long to inject the fault."),
			Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("30s"), Required: extutil.Ptr(true), Order: extutil.Ptr(0),
		},
		{
			Name: "percentage", Label: "Percentage", Description: extutil.Ptr("Percentage of matching connections the fault is applied to."),
			Type: action_kit_api.ActionParameterTypePercentage, DefaultValue: extutil.Ptr("50"), Required: extutil.Ptr(true), Order: extutil.Ptr(1),
		},
		{
			Name: "hostname", Label: "Dependency Hostnames", Description: extutil.Ptr("Apply the fault only to connections to these hosts (TLS SNI or HTTP Host, subdomain match). If empty, all intercepted connections are faulted."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(10),
		},
		{
			Name: "ip", Label: "Dependency CIDRs", Description: extutil.Ptr("Intercept traffic destined for these IP CIDRs. Defaults to all destinations. Note: interception is currently IPv4 only."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(11), Advanced: extutil.Ptr(true),
		},
		{
			Name: "port", Label: "Dependency Ports", Description: extutil.Ptr("Comma-separated destination ports to intercept."),
			Type: action_kit_api.ActionParameterTypeString, DefaultValue: extutil.Ptr(portsDefault), Required: extutil.Ptr(false), Order: extutil.Ptr(12), Advanced: extutil.Ptr(true),
		},
		{
			Name: "excludeIp", Label: "Exclude CIDRs", Description: extutil.Ptr("Never intercept traffic to these CIDRs (in addition to the automatically protected agent/extension endpoints)."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(13), Advanced: extutil.Ptr(true),
		},
		{
			Name: "failOnHostNetwork", Label: "Fail on Host Network", Description: extutil.Ptr("Fail if the target container uses the host network."),
			Type: action_kit_api.ActionParameterTypeBoolean, DefaultValue: extutil.Ptr("true"), Required: extutil.Ptr(false), Order: extutil.Ptr(14), Advanced: extutil.Ptr(true),
		},
	}
}

func (a *dependencyFaultAction) parseOpts(request action_kit_api.PrepareActionRequestBody) (proxyfault.Opts, error) {
	cfg := request.Config

	ports, err := parsePortsList(extutil.ToString(cfg["port"]))
	if err != nil {
		return proxyfault.Opts{}, err
	}

	includeCIDRs, err := parseCIDRArgs(extutil.ToStringArray(cfg["ip"]))
	if err != nil {
		return proxyfault.Opts{}, fmt.Errorf("invalid dependency CIDR: %w", err)
	}
	if len(includeCIDRs) == 0 {
		includeCIDRs = network.NetAny // 0.0.0.0/0 + ::/0 (proxy currently intercepts IPv4)
	}

	excludeCIDRs, err := a.buildExcludes(request)
	if err != nil {
		return proxyfault.Opts{}, err
	}

	fault, err := a.spec.buildFault(cfg)
	if err != nil {
		return proxyfault.Opts{}, err
	}
	fault.Hosts = extutil.ToStringArray(cfg["hostname"])
	fault.Probability = percentageToProbability(cfg["percentage"])

	duration := time.Duration(extutil.ToInt64(cfg["duration"])) * time.Millisecond

	return proxyfault.Opts{
		ExecutionId: request.ExecutionId.String()[24:],
		// ProxyPort 0: the proxy binds an OS-chosen port and targets its own
		// interception at that exact port, avoiding a pre-allocation race.
		ProxyPort:    0,
		MaxDuration:  duration + 30*time.Second, // deadman if Stop never arrives
		IncludeCIDRs: includeCIDRs,
		ExcludeCIDRs: excludeCIDRs,
		Ports:        ports,
		Fault:        fault,
	}, nil
}

// percentageToProbability maps a 0..100 percentage to a 0..1 probability,
// clamped. 100 (or missing/invalid) maps to 1.0 (always).
func percentageToProbability(v any) float64 {
	var pct float64
	switch n := v.(type) {
	case float64:
		pct = n
	case float32:
		pct = float64(n)
	case int:
		pct = float64(n)
	case int64:
		pct = float64(n)
	default:
		return 1.0
	}
	p := pct / 100.0
	if p <= 0 || p >= 1 {
		return 1.0
	}
	return p
}

func (a *dependencyFaultAction) buildExcludes(request action_kit_api.PrepareActionRequestBody) ([]net.IPNet, error) {
	var nwps []network.NetWithPortRange
	restricted, err := toExcludes(getRestrictedEndpoints(request))
	if err != nil {
		return nil, err
	}
	nwps = append(nwps, restricted...)
	nwps = append(nwps, network.ComputeExcludesForOwnIpAndPorts(config.Config.Port, config.Config.HealthPort)...)

	// User excludes are a protective list — a malformed entry must error, not be
	// silently dropped (which would let faulted traffic reach a dependency the
	// user meant to protect).
	userCIDRs, err := parseCIDRArgs(extutil.ToStringArray(request.Config["excludeIp"]))
	if err != nil {
		return nil, fmt.Errorf("invalid excludeIp: %w", err)
	}
	nwps = append(nwps, network.NewNetWithPortRanges(userCIDRs, network.PortRangeAny)...)

	// Condense to bound the resulting --exclude-cidrs arg length / rule count.
	nwps = netfault.CondenseNetWithPortRange(nwps, 500)

	// proxyfault excludes are IP-only, so port-scoped restricted endpoints are
	// intentionally widened to the whole IP (safe over-exclusion). Dedupe.
	seen := make(map[string]struct{}, len(nwps))
	out := make([]net.IPNet, 0, len(nwps))
	for _, n := range nwps {
		if _, ok := seen[n.Net.String()]; ok {
			continue
		}
		seen[n.Net.String()] = struct{}{}
		out = append(out, n.Net)
	}
	return out, nil
}

func parsePortsList(s string) ([]uint16, error) {
	var out []uint16
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q: %w", p, err)
		}
		out = append(out, uint16(n))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one intercept port is required")
	}
	return out, nil
}

// parseCIDRArgs parses each entry with the shared network.ParseCIDR (which also
// accepts bare IPs), erroring on any malformed entry so a mistyped selector is
// never silently dropped.
func parseCIDRArgs(items []string) ([]net.IPNet, error) {
	var out []net.IPNet
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := network.ParseCIDR(s)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, nil
}

func getDependencyFaultHandle(executionId string) (*proxyHandle, bool) {
	dependencyFaultHandlesLock.Lock()
	defer dependencyFaultHandlesLock.Unlock()
	h, ok := dependencyFaultHandles[executionId]
	return h, ok
}

func removeDependencyFaultHandle(executionId string) {
	dependencyFaultHandlesLock.Lock()
	defer dependencyFaultHandlesLock.Unlock()
	delete(dependencyFaultHandles, executionId)
}
