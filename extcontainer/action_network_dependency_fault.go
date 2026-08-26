// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"net"
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

var _ action_kit_sdk.Action[DependencyFaultState] = (*dependencyFaultAction)(nil)
var _ action_kit_sdk.ActionWithStatus[DependencyFaultState] = (*dependencyFaultAction)(nil)
var _ action_kit_sdk.ActionWithStop[DependencyFaultState] = (*dependencyFaultAction)(nil)

// proxyHandle keeps the running proxy plus what's needed for an out-of-band
// revert (the same Opts reproduce the chain names; the sidecar reaches the
// target container's netns). Kept in memory only, like dns-inject.
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
}

func NewNetworkDependencyFaultAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[DependencyFaultState] {
	return &dependencyFaultAction{ociRuntime: r, client: client}
}

func (a *dependencyFaultAction) NewEmptyState() DependencyFaultState {
	return DependencyFaultState{}
}

func (a *dependencyFaultAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_dependency_fault", BaseActionID),
		Label:       "Fault Dependency Traffic",
		Description: "Transparently proxy the container's outgoing traffic to a dependency and inject a fault (latency, connection reset, or an HTTP error status) selected by hostname/SNI.",
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
		Parameters: dependencyFaultParameters(),
	}
}

func (a *dependencyFaultAction) Prepare(ctx context.Context, state *DependencyFaultState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
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

	// Terminate the proxy: its in-process Guard tears down the interception.
	if err := handle.proxy.Stop(); err != nil {
		log.Warn().Err(err).Str("execution_id", state.ExecutionId).Msg("failed to stop transparent-proxy")
	}

	// Belt-and-suspenders: an idempotent out-of-band revert removes the rules
	// even if the proxy was killed before its Guard could run.
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

func dependencyFaultParameters() []action_kit_api.ActionParameter {
	return []action_kit_api.ActionParameter{
		{
			Name: "duration", Label: "Duration", Description: extutil.Ptr("How long to inject the fault."),
			Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("30s"), Required: extutil.Ptr(true), Order: extutil.Ptr(0),
		},
		{
			Name: "faultType", Label: "Fault Type", Description: extutil.Ptr("Which fault to inject on matching connections."),
			Type: action_kit_api.ActionParameterTypeString, DefaultValue: extutil.Ptr("latency"), Required: extutil.Ptr(true), Order: extutil.Ptr(1),
			Options: extutil.Ptr([]action_kit_api.ParameterOption{
				action_kit_api.ExplicitParameterOption{Label: "Latency", Value: "latency"},
				action_kit_api.ExplicitParameterOption{Label: "Connection Reset", Value: "reset"},
				action_kit_api.ExplicitParameterOption{Label: "HTTP Error Status", Value: "http_status"},
			}),
		},
		{
			Name: "networkLatency", Label: "Latency", Description: extutil.Ptr("Latency to add (for the Latency fault type)."),
			Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("500ms"), Required: extutil.Ptr(false), Order: extutil.Ptr(2),
		},
		{
			Name: "httpStatus", Label: "HTTP Status", Description: extutil.Ptr("HTTP status code to return (for the HTTP Error Status fault type, cleartext HTTP only)."),
			Type: action_kit_api.ActionParameterTypeInteger, DefaultValue: extutil.Ptr("503"), Required: extutil.Ptr(false), Order: extutil.Ptr(3),
		},
		{
			Name: "hostname", Label: "Dependency Hostnames", Description: extutil.Ptr("Apply the fault only to connections to these hosts (TLS SNI or HTTP Host, subdomain match). If empty, all intercepted connections are faulted."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(4),
		},
		{
			Name: "ip", Label: "Dependency CIDRs", Description: extutil.Ptr("Intercept traffic destined for these IP CIDRs. Defaults to all destinations. Note: interception is currently IPv4 only."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(5), Advanced: extutil.Ptr(true),
		},
		{
			Name: "port", Label: "Dependency Ports", Description: extutil.Ptr("Comma-separated destination ports to intercept."),
			Type: action_kit_api.ActionParameterTypeString, DefaultValue: extutil.Ptr("80,443"), Required: extutil.Ptr(false), Order: extutil.Ptr(6), Advanced: extutil.Ptr(true),
		},
		{
			Name: "excludeIp", Label: "Exclude CIDRs", Description: extutil.Ptr("Never intercept traffic to these CIDRs (in addition to the automatically protected agent/extension endpoints)."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(7), Advanced: extutil.Ptr(true),
		},
		{
			Name: "failOnHostNetwork", Label: "Fail on Host Network", Description: extutil.Ptr("Fail if the target container uses the host network."),
			Type: action_kit_api.ActionParameterTypeBoolean, DefaultValue: extutil.Ptr("true"), Required: extutil.Ptr(false), Order: extutil.Ptr(8), Advanced: extutil.Ptr(true),
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

	fault, err := parseFault(cfg)
	if err != nil {
		return proxyfault.Opts{}, err
	}

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

func parseFault(cfg map[string]any) (proxyfault.Fault, error) {
	hosts := extutil.ToStringArray(cfg["hostname"])
	switch extutil.ToString(cfg["faultType"]) {
	case "latency":
		latency := time.Duration(extutil.ToInt64(cfg["networkLatency"])) * time.Millisecond
		if latency <= 0 {
			return proxyfault.Fault{}, fmt.Errorf("networkLatency must be greater than 0 for the Latency fault type")
		}
		return proxyfault.Fault{Hosts: hosts, Latency: latency}, nil
	case "reset":
		return proxyfault.Fault{Hosts: hosts, AbortProbability: 1.0}, nil
	case "http_status":
		status := int(extutil.ToInt64(cfg["httpStatus"]))
		if status < 100 || status > 599 {
			return proxyfault.Fault{}, fmt.Errorf("httpStatus must be within [100,599], got %d", status)
		}
		return proxyfault.Fault{Hosts: hosts, HTTPStatus: status}, nil
	default:
		return proxyfault.Fault{}, fmt.Errorf("unknown faultType %q", extutil.ToString(cfg["faultType"]))
	}
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
