// Copyright 2025 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkPackageLossContainerAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: packageLoss(r),
		optsDecoder:  packageLossDecode,
		description:  getNetworkPackageLossDescription(),
		ociRuntime:   r,
		client:       client,
	}
}

func getNetworkPackageLossDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_package_loss", BaseActionID),
		Label:       "Drop Outgoing Traffic",
		Description: "Cause packet loss for outgoing network traffic (egress).",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(lossIcon),
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
				Name:         "networkLoss",
				Label:        "Network Loss",
				Description:  extutil.Ptr("How much of the traffic should be lost?"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: extutil.Ptr("70"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			action_kit_api.ActionParameter{
				Name:        "networkInterface",
				Label:       "Network Interface",
				Description: extutil.Ptr("Target Network Interface which should be affected. All if none specified."),
				Type:        action_kit_api.ActionParameterTypeStringArray,
				Required:    extutil.Ptr(false),
				Order:       extutil.Ptr(104),
			},
		),
	}
}

func packageLoss(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar network.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (network.Opts, action_kit_api.Messages, error) {
		loss := extutil.ToUInt(request.Config["networkLoss"])

		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		interfaces := extutil.ToStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = network.ListNonLoopbackInterfaceNames(ctx, network.NewRuncRunner(r, sidecar))
			if err != nil {
				return nil, nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, nil, fmt.Errorf("no network interfaces specified")
		}

		return &network.PackageLossOpts{
			Filter:     filter,
			Loss:       loss,
			Interfaces: interfaces,
		}, messages, nil
	}
}

func packageLossDecode(data json.RawMessage) (network.Opts, error) {
	var opts network.PackageLossOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
