// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/network"
	"github.com/steadybit/extension-container/pkg/networkutils"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"net"
)

type networkOptsProvider func(ctx context.Context, sidecarConfig network.TargetContainerConfig, request action_kit_api.PrepareActionRequestBody) (networkutils.Opts, error)

type networkOptsDecoder func(data json.RawMessage) (networkutils.Opts, error)

type networkAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider networkOptsProvider
	optsDecoder  networkOptsDecoder
}

type NetworkActionState struct {
	ExecutionId     uuid.UUID
	NetworkOpts     json.RawMessage
	ContainerConfig network.TargetContainerConfig
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
		Label:        "IP Address",
		Description:  extutil.Ptr("Restrict to/from which IP addresses the traffic is affected."),
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

	if extutil.ToBool(request.Config["failOnHostNetwork"]) {
		hasHostNetwork, err := a.hasHostNetwork(ctx, containerId[0])
		if err != nil {
			return nil, extension_kit.ToError("Failed to check if container is using host network.", err)
		}
		if hasHostNetwork {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}
	}

	cfg, err := network.GetConfigForContainer(ctx, a.runc, RemovePrefix(containerId[0]))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare network settings.", err)
	}

	opts, err := a.optsProvider(ctx, cfg, request)
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare network settings.", err)
	}

	rawOpts, err := json.Marshal(opts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to serialize network settings.", err)
	}

	state.NetworkOpts = rawOpts
	state.ContainerConfig = cfg
	return nil, nil
}

func (a *networkAction) hasHostNetwork(ctx context.Context, containerId string) (bool, error) {
	containerState, err := a.runc.State(ctx, RemovePrefix(containerId))
	if err != nil {
		return false, err
	}
	return runc.HasHostNetwork(ctx, containerState.Pid)
}

func (a *networkAction) Start(ctx context.Context, state *NetworkActionState) (*action_kit_api.StartResult, error) {
	opts, err := a.optsDecoder(state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to deserialize network settings.", err)
	}

	err = network.Apply(ctx, a.runc, state.ContainerConfig, opts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to apply network settings.", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: opts.String(),
			},
		}),
	}, nil

}

func (a *networkAction) Stop(ctx context.Context, state *NetworkActionState) (*action_kit_api.StopResult, error) {
	opts, err := a.optsDecoder(state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to deserialize network settings.", err)
	}

	err = network.Revert(ctx, a.runc, state.ContainerConfig, opts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to revert network settings.", err)
	}

	return nil, nil
}

func parsePortRanges(raw []string) ([]networkutils.PortRange, error) {
	if raw == nil {
		return nil, nil
	}

	var ranges []networkutils.PortRange

	for _, r := range raw {
		parsed, err := networkutils.ParsePortRange(r)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, parsed)
	}

	return ranges, nil
}

func mapToNetworkFilter(ctx context.Context, r runc.Runc, cfg network.TargetContainerConfig, config map[string]interface{}, restrictedEndpoints []action_kit_api.RestrictedEndpoint) (networkutils.Filter, error) {
	toResolve := append(
		extutil.ToStringArray(config["ip"]),
		extutil.ToStringArray(config["hostname"])...,
	)

	includeIps, err := network.ResolveHostnames(ctx, r, cfg, toResolve...)
	if err != nil {
		return networkutils.Filter{}, err
	}

	//if no hostname/ip specified we affect all ips
	includeCidrs := networkutils.NetAny
	if len(includeIps) > 0 {
		includeCidrs = networkutils.IpToNet(includeIps)
	}

	portRanges, err := parsePortRanges(extutil.ToStringArray(config["port"]))
	if err != nil {
		return networkutils.Filter{}, err
	}
	if len(portRanges) == 0 {
		//if no hostname/ip specified we affect all ports
		portRanges = []networkutils.PortRange{networkutils.PortRangeAny}
	}

	includes := networkutils.NewNetWithPortRanges(includeCidrs, portRanges...)
	var excludes []networkutils.NetWithPortRange

	for _, restrictedEndpoint := range restrictedEndpoints {
		log.Debug().Msgf("Adding restricted endpoint %s (%s) => %s:%d-%d", restrictedEndpoint.Name, restrictedEndpoint.Url, restrictedEndpoint.Cidr, restrictedEndpoint.PortMin, restrictedEndpoint.PortMax)
		_, cidr, err := net.ParseCIDR(restrictedEndpoint.Cidr)
		if err != nil {
			return networkutils.Filter{}, fmt.Errorf("invalid cidr %s: %w", restrictedEndpoint.Cidr, err)
		}
		excludes = append(excludes, networkutils.NewNetWithPortRanges([]net.IPNet{*cidr}, networkutils.PortRange{From: uint16(restrictedEndpoint.PortMin), To: uint16(restrictedEndpoint.PortMax)})...)
	}

	return networkutils.Filter{
		Include: includes,
		Exclude: excludes,
	}, nil
}

func readNetworkInterfaces(ctx context.Context, r runc.Runc, cfg network.TargetContainerConfig) ([]string, error) {
	ifcs, err := network.ListInterfaces(ctx, r, cfg)
	if err != nil {
		return nil, err
	}

	var ifcNames []string
	for _, ifc := range ifcs {
		if ifc.HasFlag("UP") && !ifc.HasFlag("LOOPBACK") {
			ifcNames = append(ifcNames, ifc.Name)
		}
	}
	return ifcNames, nil
}
