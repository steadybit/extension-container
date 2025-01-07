// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

type pauseAction struct {
	client types.Client
}

type PauseActionState struct {
	ContainerId string
	TargetLabel string
}

// Make sure pauseAction implements all required interfaces
var _ action_kit_sdk.Action[PauseActionState] = (*pauseAction)(nil)
var _ action_kit_sdk.ActionWithStop[PauseActionState] = (*pauseAction)(nil)

func NewPauseContainerAction(client types.Client) action_kit_sdk.Action[PauseActionState] {
	return &pauseAction{
		client: client,
	}
}

func (a *pauseAction) NewEmptyState() PauseActionState {
	return PauseActionState{}
}

func (a *pauseAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.pause", BaseActionID),
		Label:       "Pause Container",
		Description: "Pauses the container for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(pauseIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("State"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should the container be paused?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("5s"),
		}),
		Stop: extutil.Ptr(action_kit_api.MutatingEndpointReference{}),
	}
}

func (a *pauseAction) Prepare(ctx context.Context, state *PauseActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	container, label, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, extension_kit.ToError("Failed to get target container", err)
	}

	state.ContainerId = container.Id()
	state.TargetLabel = label

	return nil, nil
}

func (a *pauseAction) Start(ctx context.Context, state *PauseActionState) (*action_kit_api.StartResult, error) {
	err := a.client.Pause(ctx, RemovePrefix(state.ContainerId))
	if err != nil {
		return nil, extension_kit.ToError("Failed to pause container", err)
	}
	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Pausing container %s", state.TargetLabel),
			},
		}),
	}, nil
}

func (a *pauseAction) Status(ctx context.Context, state *PauseActionState) (*action_kit_api.StatusResult, error) {
	_, err := a.client.GetPid(ctx, RemovePrefix(state.ContainerId))
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Container %s is not running anymore", state.TargetLabel),
				},
			}),
		}, nil
	}
	return &action_kit_api.StatusResult{
		Completed: false,
	}, nil
}

func (a *pauseAction) Stop(ctx context.Context, state *PauseActionState) (*action_kit_api.StopResult, error) {
	_, err := a.client.GetPid(ctx, RemovePrefix(state.ContainerId))
	if err != nil {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Container %s is not running anymore", state.TargetLabel),
				},
			}),
		}, nil
	}

	err = a.client.Unpause(ctx, RemovePrefix(state.ContainerId))
	if err != nil {
		return nil, extension_kit.ToError("Failed to unpause container", err)
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Unpaused container %s", state.TargetLabel),
			},
		}),
	}, nil
}
