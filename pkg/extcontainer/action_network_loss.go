// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/networkutils"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/network"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkPackageLossContainerAction(r runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: packageLoss(r),
		optsDecoder:  packageLossDecode,
		description:  getNetworkPackageLossDescription(),
		runc:         r,
	}
}

func getNetworkPackageLossDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_package_loss", BaseActionID),
		Label:       "Drop Outgoing Traffic",
		Description: "Cause packet loss for outgoing network traffic (egress).",
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
				Name:         "networkLoss",
				Label:        "Network Loss",
				Description:  extutil.Ptr("How much of the traffic should be lost?"),
				Type:         action_kit_api.Percentage,
				DefaultValue: extutil.Ptr("70"),
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

func packageLoss(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, cfg network.TargetContainerConfig, request action_kit_api.PrepareActionRequestBody) (networkutils.Opts, error) {
		loss := extutil.ToUInt(request.Config["networkLoss"])

		filter, err := mapToNetworkFilter(ctx, r, cfg, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, err
		}

		interfaces := extutil.ToStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = readNetworkInterfaces(ctx, r, cfg)
			if err != nil {
				return nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, fmt.Errorf("no network interfaces specified")
		}

		return &networkutils.PackageLossOpts{
			Filter:     filter,
			Loss:       loss,
			Interfaces: interfaces,
		}, nil
	}
}

func packageLossDecode(data json.RawMessage) (networkutils.Opts, error) {
	var opts networkutils.PackageLossOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
