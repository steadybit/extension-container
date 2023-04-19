// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/network"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"net/url"
	"strconv"
	"strings"
)

type networkOptsProvider func(ctx context.Context, request action_kit_api.PrepareActionRequestBody) (network.BlackholeOpts, error)

type networkAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider networkOptsProvider
}

type NetworkActionState struct {
	ContainerId string
	NetworkOpts network.BlackholeOpts //FIXME polymorphism use NetworkOps
	ExecutionId uuid.UUID
}

// Make sure networkAction implements all required interfaces
var _ action_kit_sdk.Action[NetworkActionState] = (*networkAction)(nil)
var _ action_kit_sdk.ActionWithStop[NetworkActionState] = (*networkAction)(nil)

func (a *networkAction) NewEmptyState() NetworkActionState {
	return NetworkActionState{}
}

func (a *networkAction) Describe() action_kit_api.ActionDescription {
	return a.description
}

func (a *networkAction) Prepare(ctx context.Context, state *NetworkActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	containerId := request.Target.Attributes["container.id"]
	if containerId == nil || len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}

	if failOnHostNetwork, ok := request.Config["failOnHostNetwork"].(bool); ok && failOnHostNetwork {
		hasHostNetwork, err := a.hasHostNetwork(ctx, containerId[0])
		if err != nil {
			return nil, extension_kit.ToError("Failed to check if container is using host network.", err)
		}
		if hasHostNetwork {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}
	}

	opts, err := a.optsProvider(ctx, request)
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare network settings.", err)
	}

	state.ContainerId = containerId[0]
	state.NetworkOpts = opts
	return nil, nil
}

func (a *networkAction) hasHostNetwork(ctx context.Context, containerId string) (bool, error) {
	containerState, err := a.runc.State(ctx, RemovePrefix(containerId))
	if err != nil {
		return false, err
	}
	return runc.HasHostNetwork(ctx, containerState.Pid)
}

func (a *networkAction) Start(ctx context.Context, state *NetworkActionState) (*action_kit_api.StartResult, error) {
	err := network.Apply(ctx, a.runc, RemovePrefix(state.ContainerId), &state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to apply network settings.", err)
	}

	var sb strings.Builder
	sb.WriteString("Blocking traffic for container ")
	sb.WriteString(state.ContainerId)
	sb.WriteString("\nto/from:")
	for _, inc := range state.NetworkOpts.Include {
		sb.WriteString(" ")
		sb.WriteString(inc.String())
		sb.WriteString("\n")
	}
	if len(state.NetworkOpts.Exclude) > 0 {
		sb.WriteString("but not from/to:")
		sb.WriteString("\n")
		for _, exc := range state.NetworkOpts.Exclude {
			sb.WriteString(" ")
			sb.WriteString(exc.String())
			sb.WriteString("\n")
		}
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: sb.String(),
			},
		}),
	}, nil

}

func (a *networkAction) Stop(ctx context.Context, state *NetworkActionState) (*action_kit_api.StopResult, error) {
	err := network.Revert(ctx, a.runc, RemovePrefix(state.ContainerId), &state.NetworkOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to revert network settings.", err)
	}

	return nil, nil
}

func parsePortRanges(raw []string) ([]network.PortRange, error) {
	if raw == nil {
		return nil, nil
	}

	var ranges []network.PortRange

	for _, r := range raw {
		r, err := network.ParsePortRange(r)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, r)
	}

	return ranges, nil
}

func resolveUrl(ctx context.Context, runc runc.Runc, containerId string, raw string) ([]string, int, error) {
	port := 0
	u, err := url.Parse(raw)

	ips, err := network.ResolveHostnames(ctx, runc, containerId, u.Hostname())
	if err != nil {
		return nil, port, err
	}

	portStr := u.Port()
	if portStr != "" {
		port, _ = strconv.Atoi(portStr)
	} else {
		switch u.Scheme {
		case "https":
			port = 443
		default:
			port = 80
		}
	}

	return ips, port, nil
}
