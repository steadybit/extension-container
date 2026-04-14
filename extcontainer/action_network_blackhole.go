// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
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
		Parameters:  commonNetworkParameters,
	}
}

func blackhole(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error) {
		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		return &netfault.BlackholeOpts{
			Filter:           filter,
			ExecutionContext: mapToExecutionContext(request),
		}, messages, nil
	}
}

func blackholeDecode(data json.RawMessage) (netfault.Opts, error) {
	var opts netfault.BlackholeOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
