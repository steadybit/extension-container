// Copyright 2025 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/dnsresolve"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
)

type networkOptsProvider func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error)

type networkOptsDecoder func(data json.RawMessage) (netfault.Opts, error)

type networkAction struct {
	ociRuntime   ociruntime.OciRuntime
	client       types.Client
	description  action_kit_api.ActionDescription
	optsProvider networkOptsProvider
	optsDecoder  networkOptsDecoder
}

type NetworkActionState struct {
	ExecutionId uuid.UUID
	NetworkOpts json.RawMessage
	Sidecar     netfault.SidecarOpts
	ContainerID string
	TargetLabel string
	// QdiscSnapshot holds the pre-attack qdisc tree captured by netfault.Apply.
	// It travels through the action_kit_sdk per-execution state so Stop can
	// hand it back to netfault.Revert and restore the original (cloud-tuned)
	// root after the attack tree is torn down. Empty when strict-mode is on,
	// the attack doesn't touch a tc root, or the capture itself errored.
	QdiscSnapshot netfault.QdiscSnapshot
	// IsShadow is true when this action's Start found another concurrent
	// action already attacking the same netns WITH IDENTICAL OPTS
	// (multi-container pods share a netns — see netns_dedup.go). Shadow
	// actions do NOT install their own tc rules; only the first (primary)
	// Start applies. Stop mirrors the same split: the primary reverts,
	// shadows no-op. Different opts on a sibling take the passthrough
	// branch instead of shadowing, so this stays false there. The flag
	// lives on state so the primary/shadow decision survives an extension
	// pod restart between Start and Stop.
	IsShadow bool
	// NetnsClaimed records whether Start incremented the netns tracker
	// counter for this action. True for primary and shadow (both hold a
	// claim). False for passthrough (netns was already claimed with
	// different opts, so we didn't touch the tracker). Stop uses this to
	// decide whether it owns a release — without it, an opts-decode or
	// revert error inside Stop would leak the claim for the rest of the
	// process's life and silently shadow every future Start on this netns.
	NetnsClaimed bool
}

// Make sure networkAction implements all required interfaces
var _ action_kit_sdk.Action[NetworkActionState] = (*networkAction)(nil)
var _ action_kit_sdk.ActionWithStop[NetworkActionState] = (*networkAction)(nil)

var commonNetworkParameters = []action_kit_api.ActionParameter{
	{
		Name:         "duration",
		Label:        "Duration",
		Description:  new("How long should the network be affected?"),
		Type:         action_kit_api.ActionParameterTypeDuration,
		DefaultValue: new("30s"),
		Required:     new(true),
		Order:        new(0),
	},
	{
		Name:         "failOnHostNetwork",
		Label:        "Fail on Host Network",
		Description:  new("Should the action fail if the container is using host network?"),
		Type:         action_kit_api.ActionParameterTypeBoolean,
		DefaultValue: new("true"),
		Required:     new(true),
		Order:        new(100),
	},
	{
		Name:         "hostname",
		Label:        "Include Hostnames",
		Description:  new("Restrict to/from which hosts the traffic is affected."),
		Type:         action_kit_api.ActionParameterTypeStringArray,
		DefaultValue: new(""),
		Advanced:     new(true),
		Order:        new(101),
	},
	{
		Name:         "ip",
		Label:        "Include IPs/CIDRs",
		Description:  new("Restrict to/from which IP addresses or blocks the traffic is affected."),
		Type:         action_kit_api.ActionParameterTypeStringArray,
		DefaultValue: new(""),
		Advanced:     new(true),
		Order:        new(102),
	},
	{
		Name:         "port",
		Label:        "Include Ports",
		Description:  new("Restrict to/from which ports the traffic is affected."),
		Type:         action_kit_api.ActionParameterTypeStringArray,
		DefaultValue: new(""),
		Advanced:     new(true),
		Order:        new(103),
	},
	{
		Name:        "excludeIp",
		Label:       "Exclude IPs/CIDRs",
		Description: new("Exclude traffic to/from these IP addresses or CIDR blocks from being affected. Excludes always take precedence over the include restrictions above (hostnames, IPs/CIDRs, ports), e.g. affect all traffic except 10.0.0.0/8."),
		Type:        action_kit_api.ActionParameterTypeStringArray,
		Required:    new(false),
		Advanced:    new(true),
		Order:       new(104),
	},
}

func (a *networkAction) NewEmptyState() NetworkActionState {
	return NetworkActionState{}
}

func (a *networkAction) Describe() action_kit_api.ActionDescription {
	return a.description
}

func (a *networkAction) Prepare(ctx context.Context, state *NetworkActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	container, label, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, extension_kit.ToError("Failed to get target container", err)
	}

	state.ContainerID = container.Id()
	state.TargetLabel = label

	processInfo, err := getProcessInfoForContainer(ctx, a.ociRuntime, RemovePrefix(state.ContainerID), specs.NetworkNamespace)
	if err != nil {
		return nil, extension_kit.ToError("Failed to read target process info", err)
	}

	state.Sidecar = netfault.SidecarOpts{
		TargetProcess: processInfo,
		Id:            fmt.Sprintf("%s-%s", request.ExecutionId.String()[24:], RemovePrefix(state.ContainerID)[:8]),
	}

	if isUsingHostNetwork(processInfo.Namespaces) {
		if config.Config.DisallowHostNetwork {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network. This is disallowed by your system administrators.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}

		if extutil.ToBool(request.Config["failOnHostNetwork"]) {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network and failOnHostNetwork = true.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}
	}

	opts, messages, err := a.optsProvider(ctx, state.Sidecar, request)
	if err != nil {
		return nil, extension_kit.WrapError(err)
	}

	if err := netfault.PreflightCheck(ctx, netfault.NewRuncRunner(a.ociRuntime, state.Sidecar), opts); err != nil {
		return nil, extension_kit.ToError("Cannot start network attack.", err)
	}

	rawOpts, err := json.Marshal(opts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to serialize network settings.", err)
	}

	state.NetworkOpts = rawOpts
	state.ExecutionId = request.ExecutionId
	return &action_kit_api.PrepareResult{Messages: &messages}, nil
}

func hasDisallowedK8sNamespaceLabel(labels map[string]string) bool {
	ns, ok := labels["io.kubernetes.pod.namespace"]
	if !ok {
		return false
	}

	return slices.ContainsFunc(config.Config.DisallowK8sNamespaces, func(d config.DisallowedName) bool {
		return d.Match(ns)
	})
}

func isUsingHostNetwork(ns []ociruntime.LinuxNamespace) bool {
	for _, n := range ns {
		if n.Type == specs.NetworkNamespace {
			return n.Path == "/proc/1/ns/net"
		}
	}
	return true
}

func (a *networkAction) Start(ctx context.Context, state *NetworkActionState) (*action_kit_api.StartResult, error) {
	opts, err := a.optsDecoder(state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to deserialize network settings.", err)
	}

	result := action_kit_api.StartResult{Messages: &action_kit_api.Messages{
		{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: opts.String(),
		},
	}}

	// Coordinate with sibling containers that share this netns (Kubernetes
	// pods do this by default — see netns_dedup.go). Three outcomes:
	//   Primary: first Start on this netns — apply the attack.
	//   Shadow: sibling already applied with IDENTICAL opts — skip apply.
	//   Passthrough: sibling already applied with DIFFERENT opts — proceed
	//     to netfault, which will accept as compatible or reject with a
	//     visible error (matches pre-PR behavior for combined attacks).
	nsID := netNsID(state.Sidecar.TargetProcess)
	switch claimNetnsForAttack(nsID, state.NetworkOpts) {
	case ClaimShadow:
		state.IsShadow = true
		state.NetnsClaimed = true
		log.Info().
			Str("containerId", state.ContainerID).
			Str("netNs", nsID).
			Msg("skipping network attack apply — sibling container on the same netns already applied this exact attack (shadow)")
		result.Messages = new(append(*result.Messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Skipping apply for container %s — a sibling container in the same pod is already running this attack on the shared network namespace.", state.TargetLabel),
		}))
		return &result, nil
	case ClaimPrimary:
		state.NetnsClaimed = true
	case ClaimPassthrough:
		// netns has an active attack with DIFFERENT opts. Don't dedup —
		// let netfault decide. state.NetnsClaimed stays false so Stop
		// doesn't try to release a counter we never took.
		log.Debug().
			Str("containerId", state.ContainerID).
			Str("netNs", nsID).
			Msg("sibling container on the same netns is running a different attack; passing this Start through to netfault")
	}

	snap, err := netfault.Apply(ctx, netfault.NewRuncRunner(a.ociRuntime, state.Sidecar), opts)
	state.QdiscSnapshot = snap
	if err != nil {
		// Apply failed — release the netns claim (if we took one) so
		// subsequent Starts have a chance to try. Without this a failed
		// primary would permanently block sibling containers from being
		// attacked in the same extension-container process. Passthrough
		// never claimed, so nothing to release there.
		if state.NetnsClaimed {
			releaseNetnsForAttack(nsID)
			state.NetnsClaimed = false
		}
		var toomany *netfault.ErrTooManyTcCommands
		if errors.As(err, &toomany) {
			result.Messages = new(append(*result.Messages, action_kit_api.Message{
				Level:   extutil.Ptr(action_kit_api.Error),
				Message: fmt.Sprintf("Too many tc commands (%d) generated. This happens when too many excludes for steadybit agent and extensions are needed. Please configure a more specific attack by adding ports, and/or CIDRs to the parameters.", toomany.Count),
			}))
			return &result, nil
		}
		return &result, extension_kit.ToError("Failed to apply network settings.", err)
	}

	return &result, nil
}

func (a *networkAction) Stop(_ context.Context, state *NetworkActionState) (*action_kit_api.StopResult, error) {
	ctx := context.Background() // don't use the context as the action should be stopped even if the request context is cancelled

	// Release the netns claim once at the end of Stop, regardless of which
	// exit path we take. Guarded by NetnsClaimed so a passthrough Start
	// (never claimed) doesn't accidentally release someone else's claim,
	// and to keep the release exactly once even on the opts-decode /
	// revert-error early returns below.
	if state.NetnsClaimed {
		defer func() {
			releaseNetnsForAttack(netNsID(state.Sidecar.TargetProcess))
		}()
	}

	// Shadow actions never applied — their Start deferred to a sibling
	// container's primary. Their Stop must not revert, or the primary's
	// attack would be torn down early. See netns_dedup.go for the full
	// primary/shadow model.
	if state.IsShadow {
		log.Info().
			Str("containerId", state.ContainerID).
			Msg("skipping network attack revert — this container was a shadow (sibling primary owns the attack)")
		return &action_kit_api.StopResult{
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Skipping revert for container %s — a sibling container in the same pod owns the attack on the shared network namespace.", state.TargetLabel),
				},
			},
		}, nil
	}

	opts, err := a.optsDecoder(state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to deserialize network settings.", err)
	}

	// Skip the rollback if the target network namespace is not present anymore and hence don't need to be reverted.
	if nsExistsErr := ociruntime.NamespacesExists(ctx, state.Sidecar.TargetProcess.Namespaces, specs.NetworkNamespace); nsExistsErr != nil {
		log.Info().
			Err(nsExistsErr).
			Str("containerId", state.ContainerID).
			Msg("target network namespace does not exist anymore, no revert necessary")

		return &action_kit_api.StopResult{
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Ingoring errors from revert network config. Target container %s exited? %s", state.TargetLabel, nsExistsErr),
				},
			},
		}, nil
	}

	if err := netfault.Revert(ctx, netfault.NewRuncRunner(a.ociRuntime, state.Sidecar), opts, state.QdiscSnapshot); err != nil {
		return nil, extension_kit.ToError("Failed to revert network settings.", err)
	}
	return nil, nil
}

func parsePortRanges(raw []string) ([]network.PortRange, error) {
	if raw == nil {
		return nil, nil
	}

	var ranges []network.PortRange

	for _, r := range raw {
		if len(r) == 0 {
			continue
		}
		parsed, err := network.ParsePortRange(r)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, parsed)
	}

	return ranges, nil
}

func mapToNetworkFilter(ctx context.Context, r ociruntime.OciRuntime, sidecar netfault.SidecarOpts, actionConfig map[string]any, restrictedEndpoints []action_kit_api.RestrictedEndpoint) (netfault.Filter, action_kit_api.Messages, error) {
	includeCidrs, unresolved := network.ParseCIDRs(append(
		extutil.ToStringArray(actionConfig["ip"]),
		extutil.ToStringArray(actionConfig["hostname"])...,
	))

	resolved, err := dnsresolve.NewDigRunc(r, sidecar.TargetProcess).Resolve(ctx, unresolved...)
	if err != nil {
		return netfault.Filter{}, nil, err
	}
	includeCidrs = append(includeCidrs, network.IpsToNets(resolved)...)

	//if no hostname/ip specified we affect all ips
	if len(includeCidrs) == 0 {
		includeCidrs = network.NetAny
	}

	portRanges, err := parsePortRanges(extutil.ToStringArray(actionConfig["port"]))
	if err != nil {
		return netfault.Filter{}, nil, err
	}
	if len(portRanges) == 0 {
		//if no hostname/ip specified we affect all ports
		portRanges = []network.PortRange{network.PortRangeAny}
	}

	includes := network.NewNetWithPortRanges(includeCidrs, portRanges...)
	for i := range includes {
		includes[i].Comment = "parameters"
	}

	slices.SortFunc(includes, network.NetWithPortRange.Compare)

	excludes, err := toExcludes(restrictedEndpoints)
	if err != nil {
		return netfault.Filter{}, nil, err
	}

	excludeCidrs, unresolvedExcludes := network.ParseCIDRs(extutil.ToStringArray(actionConfig["excludeIp"]))
	resolvedExcludes, err := dnsresolve.NewDigRunc(r, sidecar.TargetProcess).Resolve(ctx, unresolvedExcludes...)
	if err != nil {
		return netfault.Filter{}, nil, err
	}
	excludeCidrs = append(excludeCidrs, network.IpsToNets(resolvedExcludes)...)

	userExcludes := network.NewNetWithPortRanges(excludeCidrs, network.PortRangeAny)
	for i := range userExcludes {
		userExcludes[i].Comment = "parameters"
	}
	excludes = append(excludes, userExcludes...)

	excludes = append(excludes, network.ComputeExcludesForOwnIpAndPorts(config.Config.Port, config.Config.HealthPort)...)

	slices.SortFunc(excludes, network.NetWithPortRange.Compare)

	var messages []action_kit_api.Message
	excludes, condensed := condenseExcludes(excludes)
	if condensed {
		messages = append(messages, action_kit_api.Message{
			Level: extutil.Ptr(action_kit_api.Warn),
			Message: "Some excludes (to protect agent and extensions) were aggregated to reduce the number of tc commands necessary." +
				"This may lead to less specific exclude rules, some traffic might not be affected, as expected. " +
				"You can avoid this by configuring a more specific attack (e.g. by specifying ports or CIDRs).",
		})
	}

	return netfault.Filter{Include: includes, Exclude: excludes}, messages, nil
}

func condenseExcludes(excludes []network.NetWithPortRange) ([]network.NetWithPortRange, bool) {
	l := len(excludes)
	excludes = netfault.CondenseNetWithPortRange(excludes, 500)
	return excludes, l != len(excludes)
}

func toExcludes(restrictedEndpoints []action_kit_api.RestrictedEndpoint) ([]network.NetWithPortRange, error) {
	var excludes []network.NetWithPortRange

	for _, restrictedEndpoint := range restrictedEndpoints {
		_, cidr, err := net.ParseCIDR(restrictedEndpoint.Cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid cidr %s: %w", restrictedEndpoint.Cidr, err)
		}

		nwps := network.NewNetWithPortRanges([]net.IPNet{*cidr}, network.PortRange{From: uint16(restrictedEndpoint.PortMin), To: uint16(restrictedEndpoint.PortMax)})
		for i := range nwps {
			var sb strings.Builder
			if restrictedEndpoint.Name != "" {
				sb.WriteString(restrictedEndpoint.Name)
				sb.WriteString(" ")
			}
			if restrictedEndpoint.Url != "" {
				sb.WriteString(restrictedEndpoint.Url)
			}
			nwps[i].Comment = strings.TrimSpace(sb.String())
		}

		excludes = append(excludes, nwps...)
	}
	return excludes, nil
}

func mapToExecutionContext(request action_kit_api.PrepareActionRequestBody) netfault.ExecutionContext {
	eCtx := netfault.ExecutionContext{}
	if request.ExecutionContext.ExperimentKey != nil {
		eCtx.ExperimentKey = *request.ExecutionContext.ExperimentKey
	}
	if request.ExecutionContext.ExecutionId != nil {
		eCtx.ExperimentExecutionId = *request.ExecutionContext.ExecutionId
	}
	eCtx.TargetExecutionId = request.ExecutionId.String()
	return eCtx
}
