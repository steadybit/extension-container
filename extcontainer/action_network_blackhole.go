// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
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
		Icon:        extutil.Ptr(blackHoleIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: slices.Concat(commonNetworkParameters, []action_kit_api.ActionParameter{
			{
				Name:         "ipRuleType",
				Label:        "IP Rule Type",
				Description:  extutil.Ptr("Specify iproute2 rule type to configure response."),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr(string(network.IpRuleTypeBlackhole)),
				Options: []action_kit_api.ParameterOption{
					{
						action_kit_api.ExplicitParameterOption{
							Label: "Blackhole (silently drop packets)",
							Value: string(network.IpRuleTypeBlackhole),
						},
						action_kit_api.ExplicitParameterOption{
							Label: "Unreachable (drop packets, return ICMP Network Unreachable)",
							Value: string(network.IpRuleTypeUnreachable),
						},
						action_kit_api.ExplicitParameterOption{
							Label: "Prohibit (drop packets, return ICMP Communication Prohibited)",
							Value: string(network.IpRuleTypeProhibit),
						},
					},
				},
				Advanced: extutil.Ptr(true),
			},
		}),
	}
}

func blackhole(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar network.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (network.Opts, action_kit_api.Messages, error) {
		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		ruleType := extutil.ToString(request.Config["ipRuleType"])

		return &network.BlackholeOpts{
			Filter:           filter,
			ExecutionContext: mapToExecutionContext(request),
			IpRuleType:       network.IpRuleType(ruleType),
		}, messages, nil
	}
}

func blackholeDecode(data json.RawMessage) (network.Opts, error) {
	var opts network.BlackholeOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
