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
	description: "Add latency to calls to a specific dependency, matched by hostname (TLS SNI / HTTP Host). Works over HTTPS and for CDN/cloud endpoints with shared or rotating IPs — use this to slow one named dependency, rather than Network Delay which slows all traffic to an IP.",
	extraParams: []action_kit_api.ActionParameter{{
		Name: "delay", Label: "Latency", Description: extutil.Ptr("Latency to add before forwarding to the dependency."),
		Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("500ms"), Required: extutil.Ptr(true), Order: extutil.Ptr(3),
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
	description: "Return an HTTP error status for calls to a specific dependency, matched by Host header. Cleartext HTTP only — for HTTPS dependencies use Reset Dependency Connections instead.",
	extraParams: []action_kit_api.ActionParameter{{
		Name: "httpStatus", Label: "HTTP Status", Description: extutil.Ptr("HTTP status code to return (cleartext HTTP only)."),
		Type: action_kit_api.ActionParameterTypeInteger, DefaultValue: extutil.Ptr("503"), Required: extutil.Ptr(true), Order: extutil.Ptr(3),
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
	description: "Reset (RST) connections to a specific dependency, matched by hostname (TLS SNI / HTTP Host). Works over HTTPS and for shared or rotating IPs — a hostname-targeted alternative to the packet-level Network attacks.",
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
		Category:    extutil.Ptr("Dependency"),
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

	opts, err := a.parseOpts(request)
	if err != nil {
		return nil, err
	}

	// Idempotent + race-safe: atomically claim the execution slot, unless another
	// active dependency fault already overlaps this target's network namespace
	// (containers in the same pod share it). Two overlapping faults in one netns
	// don't stack — the first-installed proxy captures the traffic — so fail with
	// a clear message instead of silently doing nothing.
	state.ExecutionId = request.ExecutionId.String()
	state.ContainerID = containerId
	reserved, conflictWith := reserveDependencyFaultHandle(state.ExecutionId, networkNamespaceInode(processInfo), opts.IncludeCIDRs, opts.Ports)
	if conflictWith != "" {
		return &action_kit_api.PrepareResult{Error: &action_kit_api.ActionKitError{
			Title:  "Another dependency fault is already active on this target's network namespace with an overlapping port/CIDR scope. Two overlapping dependency faults on the same target don't stack — the first one wins. Stop it first, or scope this fault to different ports or CIDRs.",
			Status: extutil.Ptr(action_kit_api.Failed),
		}}, nil
	}
	if !reserved {
		// A reservation/handle for this execution already exists — idempotent.
		return &action_kit_api.PrepareResult{}, nil
	}

	sidecarId := fmt.Sprintf("%s-%s", request.ExecutionId.String()[24:], containerId[:min(8, len(containerId))])
	proxy, err := proxyfault.NewProcess(ctx, a.ociRuntime, processInfo, sidecarId, opts)
	if err != nil {
		removeDependencyFaultHandle(state.ExecutionId)
		return nil, fmt.Errorf("failed to create transparent-proxy process: %w", err)
	}

	storeDependencyFaultHandle(state.ExecutionId, &proxyHandle{proxy: proxy, opts: opts, sidecar: processInfo, id: sidecarId})

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

func (a *dependencyFaultAction) Status(ctx context.Context, state *DependencyFaultState) (*action_kit_api.StatusResult, error) {
	handle, ok := getDependencyFaultHandle(state.ExecutionId)
	if !ok {
		return &action_kit_api.StatusResult{Completed: true}, nil
	}
	if exited, err := handle.proxy.Exited(); exited {
		// The proxy is gone; still run the out-of-band revert so a proxy that
		// died before its Guard could clean up doesn't leak interception rules.
		// Without this, a Stop after a Completed status finds no handle and skips
		// the safety revert entirely.
		a.teardown(ctx, state.ExecutionId, handle)
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
	a.teardown(ctx, state.ExecutionId, handle)
	return nil, nil
}

// teardown stops the proxy and runs the idempotent out-of-band revert, then
// drops the handle. It is safe to call more than once and from either Status
// (proxy exited) or Stop — removeDependencyFaultHandle makes the second call a
// no-op, and proxy.Stop / Revert are both idempotent.
func (a *dependencyFaultAction) teardown(ctx context.Context, executionId string, handle *proxyHandle) {
	// Terminate the proxy: its in-process Guard tears down the interception.
	if err := handle.proxy.Stop(); err != nil {
		log.Warn().Err(err).Str("execution_id", executionId).Msg("failed to stop transparent-proxy")
	}

	// Belt-and-suspenders: an idempotent out-of-band revert removes the rules
	// even if the proxy was killed before its Guard could run.
	runner := nsrunner.NewRuncRunner(a.ociRuntime, nsrunner.SidecarOpts{
		TargetProcess: handle.sidecar,
		Id:            handle.id,
		Env:           []string{"XTABLES_LOCKFILE=/tmp/xtables.lock"},
	})
	if err := proxyfault.Revert(ctx, runner, handle.opts); err != nil {
		log.Warn().Err(err).Str("execution_id", executionId).Msg("out-of-band interception revert failed")
	}

	removeDependencyFaultHandle(executionId)
}

// --- parameters & parsing ---

func commonDependencyParameters(portsDefault string) []action_kit_api.ActionParameter {
	return []action_kit_api.ActionParameter{
		{
			Name: "hostname", Label: "Dependency Hostnames", Description: extutil.Ptr("Apply the fault to connections to these hosts (TLS SNI or HTTP Host, subdomain match)."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(true), Order: extutil.Ptr(0),
		},
		{
			Name: "duration", Label: "Duration", Description: extutil.Ptr("How long to inject the fault."),
			Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("30s"), Required: extutil.Ptr(true), Order: extutil.Ptr(1),
		},
		{
			Name: "percentage", Label: "Percentage", Description: extutil.Ptr("Percentage of matching connections the fault is applied to."),
			Type: action_kit_api.ActionParameterTypePercentage, DefaultValue: extutil.Ptr("50"), Required: extutil.Ptr(true), Order: extutil.Ptr(2),
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
	prob := percentageToProbability(cfg["percentage"])
	fault.Probability = &prob

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
// clamped to [0,1]: 0% means never, 100% means always. A missing/invalid value
// falls back to 1.0 (always).
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
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
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
	// A nil entry is a reservation held by an in-flight Prepare; treat it as
	// absent so Start/Status/Stop never dereference an unfilled handle.
	if h == nil {
		return nil, false
	}
	return h, ok
}

// reserveDependencyFaultHandle atomically claims the execution's slot, unless an
// already-active dependency fault overlaps the same network namespace and scope.
// Returns:
//   - reserved=true: the caller now owns a fresh reservation (fill it with
//     storeDependencyFaultHandle or release with removeDependencyFaultHandle).
//   - conflictWith set: another execution already intercepts overlapping traffic
//     in the same netns; the caller should fail (two overlapping faults in one
//     netns don't stack — the first-installed proxy captures the traffic).
//   - both zero: a reservation/handle for this execution already exists
//     (idempotent retry).
//
// The check and the claim happen under one lock, so concurrent Prepares can't
// both spawn a sidecar for the same execution or miss each other's scope.
func reserveDependencyFaultHandle(executionId string, netnsInode uint64, cidrs []net.IPNet, ports []uint16) (reserved bool, conflictWith string) {
	dependencyFaultHandlesLock.Lock()
	defer dependencyFaultHandlesLock.Unlock()
	if _, exists := dependencyFaultHandles[executionId]; exists {
		return false, ""
	}
	for id, h := range dependencyFaultHandles {
		if h == nil {
			continue // an in-flight reservation with no scope/netns recorded yet
		}
		if netnsInode != 0 && netnsInode == networkNamespaceInode(h.sidecar) && scopesOverlap(h.opts, cidrs, ports) {
			return false, id
		}
	}
	dependencyFaultHandles[executionId] = nil
	return true, ""
}

// networkNamespaceInode returns the inode of the process's network namespace, or
// 0 if unknown. Processes in the same netns (containers in one pod, or all host
// processes) share this inode, so it identifies "the same netns" across targets.
func networkNamespaceInode(info ociruntime.LinuxProcessInfo) uint64 {
	for _, ns := range info.Namespaces {
		if ns.Type == specs.NetworkNamespace {
			return ns.Inode
		}
	}
	return 0
}

// scopesOverlap reports whether two interception scopes can catch the same
// traffic: their port sets and CIDR sets both intersect.
func scopesOverlap(existing proxyfault.Opts, cidrs []net.IPNet, ports []uint16) bool {
	return portsOverlap(existing.Ports, ports) && cidrsOverlap(existing.IncludeCIDRs, cidrs)
}

func portsOverlap(a, b []uint16) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func cidrsOverlap(a, b []net.IPNet) bool {
	for i := range a {
		for j := range b {
			if a[i].Contains(b[j].IP) || b[j].Contains(a[i].IP) {
				return true
			}
		}
	}
	return false
}

func storeDependencyFaultHandle(executionId string, h *proxyHandle) {
	dependencyFaultHandlesLock.Lock()
	defer dependencyFaultHandlesLock.Unlock()
	dependencyFaultHandles[executionId] = h
}

func removeDependencyFaultHandle(executionId string) {
	dependencyFaultHandlesLock.Lock()
	defer dependencyFaultHandlesLock.Unlock()
	delete(dependencyFaultHandles, executionId)
}
