// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/netfault"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkTcpResetContainerAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: tcpReset(r),
		optsDecoder:  tcpResetDecode,
		description:  getNetworkTcpResetDescription(),
		ociRuntime:   r,
		client:       client,
	}
}

func getNetworkTcpResetDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_tcp_reset", BaseActionID),
		Label:       "Reset TCP Connection",
		Description: "Injects TCP resets for matching connections (incoming and outgoing).",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(tcpResetIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  new("Container"),
		Category:    new("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: append(
			commonNetworkParameters,
			action_kit_api.ActionParameter{
				Name:        "networkInterface",
				Label:       "Network Interface",
				Description: new("Target Network Interface which should be affected. All if none specified."),
				Type:        action_kit_api.ActionParameterTypeStringArray,
				Required:    new(false),
				Advanced:    new(true),
				Order:       new(104),
			},
		),
	}
}

func tcpReset(r ociruntime.OciRuntime) networkOptsProvider {
	return func(ctx context.Context, sidecar netfault.SidecarOpts, request action_kit_api.PrepareActionRequestBody) (netfault.Opts, action_kit_api.Messages, error) {
		filter, messages, err := mapToNetworkFilter(ctx, r, sidecar, request.Config, getRestrictedEndpoints(request))
		if err != nil {
			return nil, nil, err
		}

		runner := netfault.NewRuncRunner(r, sidecar)

		interfaces := extutil.ToStringArray(request.Config["networkInterface"])
		if len(interfaces) == 0 {
			interfaces, err = netfault.ListNonLoopbackInterfaceNames(ctx, runner)
			if err != nil {
				return nil, nil, err
			}
		}

		if len(interfaces) == 0 {
			return nil, nil, fmt.Errorf("no network interfaces specified")
		}

		var useMangleChain bool
		istio, istioErr := netfault.HasIstioRedirect(ctx, runner)
		if istioErr != nil {
			log.Warn().Err(istioErr).Msg("failed to detect Istio, falling back to filter table")
		}
		if istio {
			useMangleChain = true
			messages = append(messages, action_kit_api.Message{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: "Istio sidecar detected, using mangle+filter mark-based approach for TCP reset rules.",
			})
		}

		return &netfault.TcpResetOpts{
			Filter:           filter,
			ExecutionContext: mapToExecutionContext(request),
			Interfaces:       interfaces,
			UseMangleChain:   useMangleChain,
		}, messages, nil
	}
}

func tcpResetDecode(data json.RawMessage) (netfault.Opts, error) {
	var opts netfault.TcpResetOpts
	err := json.Unmarshal(data, &opts)
	return &opts, err
}
