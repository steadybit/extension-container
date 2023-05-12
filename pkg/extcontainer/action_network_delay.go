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
	"github.com/steadybit/extension-container/pkg/networkutils"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

func NewNetworkDelayContainerAction(r runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: delay(r),
		optsDecoder:  delayDecode,
		description:  getNetworkDelayDescription(),
		runc:         r,
	}
}

func getNetworkDelayDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_delay", targetID),
		Label:       "Delay Traffic",
		Description: "Inject latency into egress network traffic.",
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
				Name:         "networkDelay",
				Label:        "Network Delay",
				Description:  extutil.Ptr("How much should the traffic be delayed?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("500ms"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			action_kit_api.ActionParameter{
				Name:         "networkDelayJitter",
				Label:        "Jitter",
				Description:  extutil.Ptr("Add random +/-30% jitter to network delay?"),
				Type:         action_kit_api.Boolean,
				DefaultValue: extutil.Ptr("true"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
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

func delay(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, cfg network.TargetContainerConfig, request action_kit_api.PrepareActionRequestBody) (networkutils.Opts, error) {
		delay := time.Duration(extutil.ToInt64(request.Config["networkDelay"])) * time.Millisecond
		hasJitter := extutil.ToBool(request.Config["networkDelayJitter"])

		jitter := 0 * time.Millisecond
		if hasJitter {
			jitter = delay * 30 / 100
		}

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

		return &networkutils.DelayOpts{
			Filter:     filter,
			Delay:      delay,
			Jitter:     jitter,
			Interfaces: interfaces,
		}, nil
	}
}

func delayDecode(data json.RawMessage) (networkutils.Opts, error) {
	var opts networkutils.DelayOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
