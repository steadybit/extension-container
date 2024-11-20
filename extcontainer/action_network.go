// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"net"
	"strings"
)

type networkOptsProvider func(ctx context.Context, sidecar network.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (network.Opts, action_kit_api.Messages, error)

type networkOptsDecoder func(data json.RawMessage) (network.Opts, error)

type networkAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider networkOptsProvider
	optsDecoder  networkOptsDecoder
}

type NetworkActionState struct {
	ExecutionId uuid.UUID
	NetworkOpts json.RawMessage
	Sidecar     network.SidecarOpts
	ContainerID string
}

// Make sure networkAction implements all required interfaces
var _ action_kit_sdk.Action[NetworkActionState] = (*networkAction)(nil)
var _ action_kit_sdk.ActionWithStop[NetworkActionState] = (*networkAction)(nil)

var commonNetworkParameters = []action_kit_api.ActionParameter{
	{
		Name:         "duration",
		Label:        "Duration",
		Description:  extutil.Ptr("How long should the network be affected?"),
		Type:         action_kit_api.Duration,
		DefaultValue: extutil.Ptr("30s"),
		Required:     extutil.Ptr(true),
		Order:        extutil.Ptr(0),
	},
	{
		Name:         "failOnHostNetwork",
		Label:        "Fail on Host Network",
		Description:  extutil.Ptr("Should the action fail if the container is using host network?"),
		Type:         action_kit_api.Boolean,
		DefaultValue: extutil.Ptr("true"),
		Required:     extutil.Ptr(true),
		Order:        extutil.Ptr(100),
	},
	{
		Name:         "hostname",
		Label:        "Hostname",
		Description:  extutil.Ptr("Restrict to/from which hosts the traffic is affected."),
		Type:         action_kit_api.StringArray,
		DefaultValue: extutil.Ptr(""),
		Advanced:     extutil.Ptr(true),
		Order:        extutil.Ptr(101),
	},
	{
		Name:         "ip",
		Label:        "IP Address/CIDR",
		Description:  extutil.Ptr("Restrict to/from which IP addresses or blocks the traffic is affected."),
		Type:         action_kit_api.StringArray,
		DefaultValue: extutil.Ptr(""),
		Advanced:     extutil.Ptr(true),
		Order:        extutil.Ptr(102),
	},
	{
		Name:         "port",
		Label:        "Ports",
		Description:  extutil.Ptr("Restrict to/from which ports the traffic is affected."),
		Type:         action_kit_api.StringArray,
		DefaultValue: extutil.Ptr(""),
		Advanced:     extutil.Ptr(true),
		Order:        extutil.Ptr(103),
	},
}

func (a *networkAction) NewEmptyState() NetworkActionState {
	return NetworkActionState{}
}

func (a *networkAction) Describe() action_kit_api.ActionDescription {
	return a.description
}

func (a *networkAction) Prepare(ctx context.Context, state *NetworkActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}
	state.ContainerID = containerId[0]

	processInfo, err := getProcessInfoForContainer(ctx, a.runc, RemovePrefix(state.ContainerID))
	if err != nil {
		return nil, extension_kit.ToError("Failed to read target process info", err)
	}

	state.Sidecar = network.SidecarOpts{
		TargetProcess: processInfo,
		IdSuffix:      RemovePrefix(state.ContainerID)[:8],
	}

	if extutil.ToBool(request.Config["failOnHostNetwork"]) && isUsingHostNetwork(processInfo.Namespaces) {
		return &action_kit_api.PrepareResult{
			Error: &action_kit_api.ActionKitError{
				Title:  "Container is using host network and failOnHostNetwork = true.",
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	opts, messages, err := a.optsProvider(ctx, state.Sidecar, request)
	if err != nil {
		return nil, extension_kit.WrapError(err)
	}

	rawOpts, err := json.Marshal(opts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to serialize network settings.", err)
	}

	state.NetworkOpts = rawOpts
	state.ExecutionId = request.ExecutionId
	return &action_kit_api.PrepareResult{Messages: &messages}, nil
}

func isUsingHostNetwork(ns []runc.LinuxNamespace) bool {
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

	err = network.Apply(ctx, a.runc, state.Sidecar, opts)
	if err != nil {
		var toomany *network.ErrTooManyTcCommands
		if errors.As(err, &toomany) {
			result.Messages = extutil.Ptr(append(*result.Messages, action_kit_api.Message{
				Level:   extutil.Ptr(action_kit_api.Error),
				Message: fmt.Sprintf("Too many tc commands (%d) generated. This happens when too many excludes for steadybit agent and extensions are needed. Please configure a more specific attack by adding ports, and/or CIDRs to the parameters.", toomany.Count),
			}))
			return &result, nil
		}
		return &result, extension_kit.ToError("Failed to apply network settings.", err)
	}

	return &result, nil
}

func (a *networkAction) Stop(ctx context.Context, state *NetworkActionState) (*action_kit_api.StopResult, error) {
	opts, err := a.optsDecoder(state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to deserialize network settings.", err)
	}

	if err := network.Revert(ctx, a.runc, state.Sidecar, opts); err == nil {
		return nil, nil
	} else if nsExistsErr := runc.NamespacesExists(ctx, state.Sidecar.TargetProcess.Namespaces, specs.NetworkNamespace); nsExistsErr == nil {
		return nil, extension_kit.ToError("Failed to revert network settings.", err)
	} else {
		log.Info().
			Str("containerId", state.ContainerID).
			AnErr("reason", nsExistsErr).
			AnErr("err", err).
			Msg("ignoring error from revert network config")

		return &action_kit_api.StopResult{
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Ingoring errors from revert network config. Target container %s exited? %s", state.ContainerID, nsExistsErr),
				},
			},
		}, nil
	}
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

func mapToNetworkFilter(ctx context.Context, r runc.Runc, sidecar network.SidecarOpts, actionConfig map[string]interface{}, restrictedEndpoints []action_kit_api.RestrictedEndpoint) (network.Filter, action_kit_api.Messages, error) {
	includeCidrs, unresolved := network.ParseCIDRs(append(
		extutil.ToStringArray(actionConfig["ip"]),
		extutil.ToStringArray(actionConfig["hostname"])...,
	))

	dig := network.HostnameResolver{Dig: &network.RuncDigRunner{Runc: r, Sidecar: sidecar}}
	resolved, err := dig.Resolve(ctx, unresolved...)
	if err != nil {
		return network.Filter{}, nil, err
	}
	includeCidrs = append(includeCidrs, network.IpsToNets(resolved)...)

	//if no hostname/ip specified we affect all ips
	if len(includeCidrs) == 0 {
		includeCidrs = network.NetAny
	}

	portRanges, err := parsePortRanges(extutil.ToStringArray(actionConfig["port"]))
	if err != nil {
		return network.Filter{}, nil, err
	}
	if len(portRanges) == 0 {
		//if no hostname/ip specified we affect all ports
		portRanges = []network.PortRange{network.PortRangeAny}
	}

	includes := network.NewNetWithPortRanges(includeCidrs, portRanges...)
	for _, i := range includes {
		i.Comment = "parameters"
	}

	excludes, err := toExcludes(restrictedEndpoints)
	if err != nil {
		return network.Filter{}, nil, err
	}
	excludes = append(excludes, network.ComputeExcludesForOwnIpAndPorts(config.Config.Port, config.Config.HealthPort)...)

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

	return network.Filter{Include: includes, Exclude: excludes}, messages, nil
}

func condenseExcludes(excludes []network.NetWithPortRange) ([]network.NetWithPortRange, bool) {
	l := len(excludes)
	excludes = network.CondenseNetWithPortRange(excludes, 500)
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
