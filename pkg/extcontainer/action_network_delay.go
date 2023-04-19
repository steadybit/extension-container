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
	"time"
)

func NewNetworkDelayContainerAction(runc runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: delay(runc),
		optsDecoder:  delayDecode,
		description:  getNetworkDelayDescription(),
		runc:         runc,
	}
}

func getNetworkDelayDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_delay", targetID),
		Label:       "Container Dela Traffic",
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
			//FIXME add interfaces
		),
	}
}

func delay(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, request action_kit_api.PrepareActionRequestBody) (network.Opts, error) {
		containerId := request.Target.Attributes["container.id"][0]
		delay := time.Duration(int(request.Config["networkDelay"].(float64))) * time.Millisecond
		hasJitter := request.Config["networkDelayJitter"] == true

		jitter := 0 * time.Millisecond
		if hasJitter {
			jitter = delay * 30 / 100
		}

		filter, err := mapToNetworkFilter(ctx, r, containerId, request.Config)
		if err != nil {
			return nil, err
		}

		return &network.DelayOpts{
			Filter:     filter,
			Delay:      delay,
			Jitter:     jitter,
			Interfaces: []string{"eth0"}, //FIXME read from config - if empty use all
		}, nil
	}
}

func delayDecode(data json.RawMessage) (network.Opts, error) {
	var opts network.DelayOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
