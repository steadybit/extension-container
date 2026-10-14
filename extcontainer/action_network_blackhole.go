// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkBlackholeContainerAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: blackhole(r),
		optsDecoder:  blackholeDecode,
		description:  getNetworkBlackholeDescription(),
		ociRuntime:   r,
		client:       client,
	}
}

func getNetworkBlackholeDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_blackhole", BaseActionID),
		Label:       "Block Traffic",
		Description: "Blocks network traffic (incoming and outgoing).",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(blackHoleIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  new("Container"),
		Category:    new("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: append(commonNetworkParameters,
			action_kit_api.ActionParameter{
				Name:         "blockLocalhost",
				Label:        "Block Localhost Traffic",
				Description:  new("Also block matching traffic on the loopback interface (127.0.0.0/8, ::1), e.g. between two processes in the same container or between containers of the same pod. Combine with the include restrictions above, otherwise all localhost traffic is blocked."),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: new("false"),
				Advanced:     new(true),
				Order:        new(106),
			},
		),
	}
}

func blackhole(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error) {
		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		blockLocalhost := extutil.ToBool(request.Config["blockLocalhost"])
		// the extension's ports are only on the target's loopback when it shares the
		// host network; otherwise they would spare the target's own traffic on them
		if blockLocalhost && isUsingHostNetwork(sidecar.TargetProcess.Namespaces) {
			filter.Exclude = append(filter.Exclude, localhostExcludesForOwnPorts()...)
		}

		return &netfault.BlackholeOpts{
			Filter:           filter,
			ExecutionContext: mapToExecutionContext(request),
			BlockLocalhost:   blockLocalhost,
		}, messages, nil
	}
}

func blackholeDecode(data json.RawMessage) (netfault.Opts, error) {
	var opts netfault.BlackholeOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}

// localhostExcludesForOwnPorts keeps the extension reachable via localhost when the
// target container shares the host network. The own-IP excludes leave out loopback
// addresses, which only matters once localhost traffic is blocked.
func localhostExcludesForOwnPorts() []network.NetWithPortRange {
	loopback := []net.IPNet{
		{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
		{IP: net.IPv6loopback, Mask: net.CIDRMask(128, 128)},
	}
	ports := []network.PortRange{{From: config.Config.Port, To: config.Config.Port}}
	if config.Config.HealthPort > 0 && config.Config.HealthPort != config.Config.Port {
		ports = append(ports, network.PortRange{From: config.Config.HealthPort, To: config.Config.HealthPort})
	}
	excludes := network.NewNetWithPortRanges(loopback, ports...)
	for i := range excludes {
		excludes[i].Comment = "ext. port (localhost)"
	}
	return excludes
}
