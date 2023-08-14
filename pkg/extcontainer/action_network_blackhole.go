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

func NewNetworkBlackholeContainerAction(runc runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: blackhole(runc),
		optsDecoder:  blackholeDecode,
		description:  getNetworkBlackholeDescription(),
		runc:         runc,
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
		Category:    extutil.Ptr("network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters:  commonNetworkParameters,
	}
}

func blackhole(r runc.Runc) networkOptsProvider {
	return func(ctx context.Context, cfg network.TargetContainerConfig, request action_kit_api.PrepareActionRequestBody) (networkutils.Opts, error) {
		filter, err := mapToNetworkFilter(ctx, r, cfg, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, err
		}

		return &networkutils.BlackholeOpts{Filter: filter}, nil
	}
}

func blackholeDecode(data json.RawMessage) (networkutils.Opts, error) {
	var opts networkutils.BlackholeOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
