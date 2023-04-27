// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/network"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkBandwidthContainerAction(runc runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: bandwidth(runc),
		optsDecoder:  bandwidthDecode,
		description:  getNetworkBandwidthDescription(),
		runc:         runc,
	}
}

func getNetworkBandwidthDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_bandwidth", targetID),
		Label:       "Limit Bandwidth",
		Description: "Limit available network bandwidth.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(targetIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.External,
		Parameters: append(
			commonNetworkParameters,
			action_kit_api.ActionParameter{
				Name:         "bandwidth",
				Label:        "Network Bandwidth",
				Description:  extutil.Ptr("How much traffic should be allowed per second?"),
				Type:         action_kit_api.String, //FIXME bitrate
				DefaultValue: extutil.Ptr("1024kbit"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			action_kit_api.ActionParameter{
				Name:        "networkInterface",
				Label:       "Network Interface",
				Description: extutil.Ptr("Target Network Interface which should be attacked."),
				Type:        action_kit_api.StringArray,
				Required:    extutil.Ptr(false),
				Order:       extutil.Ptr(104),
			},
		),
	}
}

func bandwidth(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, request action_kit_api.PrepareActionRequestBody) (network.Opts, error) {
		containerId := request.Target.Attributes["container.id"][0]
		bandwidth := extutil.ToString(request.Config["bandwidth"])

		filter, err := mapToNetworkFilter(ctx, r, containerId, request.Config)
		if err != nil {
			return nil, err
		}

		interfaces := toStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = readNetworkInterfaces(ctx, r, RemovePrefix(containerId))
			if err != nil {
				return nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, fmt.Errorf("no network interfaces specified")
		}

		return &network.BandwidthOpts{
			Filter:     filter,
			Bandwidth:  bandwidth,
			Interfaces: interfaces,
		}, nil
	}
}

func bandwidthDecode(data json.RawMessage) (network.Opts, error) {
	var opts network.BandwidthOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
