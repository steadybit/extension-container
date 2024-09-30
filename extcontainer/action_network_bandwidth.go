// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkLimitBandwidthContainerAction(runc runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: limitBandwidth(runc),
		optsDecoder:  limitBandwidthDecode,
		description:  getNetworkLimitBandwidthDescription(),
		runc:         runc,
	}
}

func getNetworkLimitBandwidthDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_bandwidth", BaseActionID),
		Label:       "Limit Bandwidth",
		Description: "Limit available network bandwidth.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(bandwidthIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: append(
			commonNetworkParameters,
			action_kit_api.ActionParameter{
				Name:         "bandwidth",
				Label:        "Network Bandwidth",
				Description:  extutil.Ptr("How much traffic should be allowed per second?"),
				Type:         action_kit_api.Bitrate,
				DefaultValue: extutil.Ptr("1024kbit"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			action_kit_api.ActionParameter{
				Name:        "networkInterface",
				Label:       "Network Interface",
				Description: extutil.Ptr("Target Network Interface which should be attacked. All if not specified."),
				Type:        action_kit_api.StringArray,
				Required:    extutil.Ptr(false),
				Order:       extutil.Ptr(104),
			},
		),
	}
}

func limitBandwidth(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, sidecar network.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (network.Opts, error) {
		bandwidth := extutil.ToString(request.Config["bandwidth"])

		filter, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, err
		}

		interfaces := extutil.ToStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = readNetworkInterfaces(ctx, r, sidecar)
			if err != nil {
				return nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, fmt.Errorf("no network interfaces specified")
		}

		return &network.LimitBandwidthOpts{
			Filter:     filter,
			Bandwidth:  bandwidth,
			Interfaces: interfaces,
		}, nil
	}
}

func limitBandwidthDecode(data json.RawMessage) (network.Opts, error) {
	var opts network.LimitBandwidthOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
