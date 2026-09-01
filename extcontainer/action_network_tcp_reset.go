// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

// networkTcpResetAction is the "Reset TCP Connection" attack. It resets matching
// connections in one of two modes, chosen by the "l7" checkbox:
//
//   - default (packet-level): iptables REJECT --reject-with tcp-reset, matching
//     by IP/port on both directions — the historical behaviour (netfault).
//   - L7: the transparent proxy resets connections selected by hostname (TLS SNI
//     / HTTP Host), so it targets a named dependency, works over HTTPS and for
//     shared/rotating IPs, and reports per-hostname statistics (proxyfault).
//
// The two engines have different state and lifecycle, so this action wraps both
// and dispatches per execution based on the chosen mode.
type networkTcpResetAction struct {
	tcp *networkAction
	l7  *dependencyFaultAction
}

// NetworkTcpResetState carries whichever sub-state the chosen mode uses.
type NetworkTcpResetState struct {
	L7         bool
	Network    NetworkActionState
	Dependency DependencyFaultState
}

var _ action_kit_sdk.Action[NetworkTcpResetState] = (*networkTcpResetAction)(nil)
var _ action_kit_sdk.ActionWithStatus[NetworkTcpResetState] = (*networkTcpResetAction)(nil)
var _ action_kit_sdk.ActionWithStop[NetworkTcpResetState] = (*networkTcpResetAction)(nil)

func NewNetworkTcpResetContainerAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[NetworkTcpResetState] {
	return &networkTcpResetAction{
		tcp: &networkAction{
			ociRuntime:   r,
			client:       client,
			optsProvider: tcpReset(r),
			optsDecoder:  tcpResetDecode,
			description:  action_kit_api.ActionDescription{},
		},
		l7: &dependencyFaultAction{ociRuntime: r, client: client, spec: resetFaultSpec},
	}
}

func (a *networkTcpResetAction) NewEmptyState() NetworkTcpResetState {
	return NetworkTcpResetState{}
}

func (a *networkTcpResetAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_tcp_reset", BaseActionID),
		Label:       "Reset TCP Connection",
		Description: "Injects TCP resets for matching connections. By default this is a packet-level reset by IP/port (incoming and outgoing). Enable 'Reset at L7 by hostname' to instead reset connections to a named dependency selected by TLS SNI / HTTP Host — which also works over HTTPS and for shared/rotating IPs.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(tcpResetIcon),
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
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.MarkdownWidget{
				Type:        action_kit_api.ComSteadybitWidgetMarkdown,
				Title:       "Dependency Fault Statistics",
				MessageType: dependencyStatsMessageType,
				Append:      false,
			},
		}),
		Parameters: tcpResetParameters(),
	}
}

// tcpResetParameters is the merged parameter set for both modes. Because action
// parameters cannot be shown/hidden by a checkbox, the two modes' parameters
// coexist; each mode ignores the ones it does not use (documented per field).
func tcpResetParameters() []action_kit_api.ActionParameter {
	return []action_kit_api.ActionParameter{
		{
			Name: "duration", Label: "Duration", Description: extutil.Ptr("How long should connections be reset?"),
			Type: action_kit_api.ActionParameterTypeDuration, DefaultValue: extutil.Ptr("30s"), Required: extutil.Ptr(true), Order: extutil.Ptr(0),
		},
		{
			Name:  "l7",
			Label: "Reset at L7 by hostname (SNI/Host)",
			Description: extutil.Ptr("Off: packet-level reset by IP/port (iptables). " +
				"On: reset a dependency selected by TLS SNI / HTTP Host — works over HTTPS and shared/rotating IPs."),
			Type: action_kit_api.ActionParameterTypeBoolean, DefaultValue: extutil.Ptr("false"), Required: extutil.Ptr(false), Order: extutil.Ptr(1),
		},
		{
			Name:  "hostname",
			Label: "Hostnames",
			Description: extutil.Ptr("In L7 mode: the dependency's hostname(s), matched by TLS SNI / HTTP Host (subdomain match) — required. " +
				"In packet-level mode: hostnames resolved to IPs to restrict which traffic is reset (optional; empty affects all)."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Order: extutil.Ptr(2),
		},
		{
			Name: "percentage", Label: "Percentage (L7 mode)", Description: extutil.Ptr("L7 mode only: percentage of matching connections to reset. Ignored in packet-level mode."),
			Type: action_kit_api.ActionParameterTypePercentage, DefaultValue: extutil.Ptr("100"), Required: extutil.Ptr(false), Order: extutil.Ptr(3),
			MinValue: extutil.Ptr(0), MaxValue: extutil.Ptr(100),
		},
		{
			Name: "ip", Label: "Include IPs/CIDRs", Description: extutil.Ptr("Restrict to/from which IP addresses or blocks the traffic is affected. In L7 mode: interception is IPv4 only."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Advanced: extutil.Ptr(true), Order: extutil.Ptr(102),
		},
		{
			Name: "port", Label: "Include Ports", Description: extutil.Ptr("Restrict to/from which ports the traffic is affected. In L7 mode an empty value defaults to 80,443."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Advanced: extutil.Ptr(true), Order: extutil.Ptr(103),
		},
		{
			Name: "excludeHostname", Label: "Exclude Hostnames", Description: extutil.Ptr("Packet-level mode only: exclude traffic to/from these hosts. Excludes take precedence over includes."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Advanced: extutil.Ptr(true), Order: extutil.Ptr(104),
		},
		{
			Name: "excludeIp", Label: "Exclude IPs/CIDRs", Description: extutil.Ptr("Exclude traffic to/from these IP addresses or CIDR blocks. Excludes take precedence over includes."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Advanced: extutil.Ptr(true), Order: extutil.Ptr(105),
		},
		{
			Name: "networkInterface", Label: "Network Interface", Description: extutil.Ptr("Packet-level mode only: target network interface(s). All if none specified."),
			Type: action_kit_api.ActionParameterTypeStringArray, Required: extutil.Ptr(false), Advanced: extutil.Ptr(true), Order: extutil.Ptr(106),
		},
	}
}

func (a *networkTcpResetAction) Prepare(ctx context.Context, state *NetworkTcpResetState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	state.L7 = extutil.ToBool(request.Config["l7"])
	if !state.L7 {
		return a.tcp.Prepare(ctx, &state.Network, request)
	}

	// L7 mode needs at least one hostname (the dependency selector). Enforced here
	// rather than in the schema, since packet-level mode allows an empty hostname.
	if len(extutil.ToStringArray(request.Config["hostname"])) == 0 {
		return &action_kit_api.PrepareResult{Error: &action_kit_api.ActionKitError{
			Title:  "L7 reset needs at least one hostname to select the dependency (TLS SNI / HTTP Host). Add a hostname, or turn off 'Reset at L7 by hostname' to reset by IP/port at the packet level.",
			Status: extutil.Ptr(action_kit_api.Failed),
		}}, nil
	}
	// The proxy requires an explicit port list; the shared 'port' param is a string
	// array (empty = "all" in packet-level mode), so normalise it to the
	// comma-separated string the dependency engine expects, defaulting to 80,443.
	// Copy the config first — mutating the caller's request.Config in place would
	// be a hidden side-effect on the shared map.
	ports := extutil.ToStringArray(request.Config["port"])
	if len(ports) == 0 {
		ports = []string{"80", "443"}
	}
	l7Request := request
	l7Request.Config = make(map[string]interface{}, len(request.Config)+1)
	for k, v := range request.Config {
		l7Request.Config[k] = v
	}
	l7Request.Config["port"] = strings.Join(ports, ",")
	return a.l7.Prepare(ctx, &state.Dependency, l7Request)
}

func (a *networkTcpResetAction) Start(ctx context.Context, state *NetworkTcpResetState) (*action_kit_api.StartResult, error) {
	if state.L7 {
		return a.l7.Start(ctx, &state.Dependency)
	}
	return a.tcp.Start(ctx, &state.Network)
}

func (a *networkTcpResetAction) Status(ctx context.Context, state *NetworkTcpResetState) (*action_kit_api.StatusResult, error) {
	if state.L7 {
		return a.l7.Status(ctx, &state.Dependency)
	}
	// Packet-level mode has no live statistics; the platform ends the step by its
	// duration or an explicit Stop. Emit a one-line note into the shared stats
	// widget so it explains itself instead of rendering an empty panel.
	return &action_kit_api.StatusResult{
		Completed: false,
		Messages: extutil.Ptr(action_kit_api.Messages{{
			Message:   "Per-hostname statistics are collected only in L7 mode (enable **Reset at L7 by hostname**). Packet-level TCP reset does not report per-connection statistics.",
			Timestamp: extutil.Ptr(time.Now()),
			Type:      extutil.Ptr(dependencyStatsMessageType),
		}}),
	}, nil
}

func (a *networkTcpResetAction) Stop(ctx context.Context, state *NetworkTcpResetState) (*action_kit_api.StopResult, error) {
	if state.L7 {
		return a.l7.Stop(ctx, &state.Dependency)
	}
	return a.tcp.Stop(ctx, &state.Network)
}

// --- packet-level (netfault) engine ---

func tcpReset(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error) {
		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		runner := netfault.NewRuncRunner(r, sidecar)

		interfaces := extutil.ToStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = netfault.ListNonLoopbackInterfaceNames(ctx, runner)
			if err != nil {
				return nil, nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, nil, fmt.Errorf("no network interfaces specified")
		}

		var useMangleChain bool
		istio, istioErr := netfault.HasIstioRedirect(ctx, runner)
		if istioErr != nil {
			log.Warn().Err(istioErr).Msg("failed to detect Istio, falling back to filter table")
		}
		if istio {
			useMangleChain = true
			messages = append(messages, action_kit_api.Message{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: "Istio sidecar detected, using mangle+filter mark-based approach for TCP reset rules.",
			})
		}

		return &netfault.TcpResetOpts{
			Filter:           filter,
			ExecutionContext: mapToExecutionContext(request),
			Interfaces:       interfaces,
			UseMangleChain:   useMangleChain,
		}, messages, nil
	}
}

func tcpResetDecode(data json.RawMessage) (netfault.Opts, error) {
	var opts netfault.TcpResetOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
