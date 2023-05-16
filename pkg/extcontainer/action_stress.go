// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/stress"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
)

type stressOptsProvider func(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error)

type stressAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider stressOptsProvider
	stresses     syncmap.Map
}

type StressActionState struct {
	ContainerId string
	StressOpts  stress.StressOpts
	ExecutionId uuid.UUID
}

// Make sure stressAction implements all required interfaces
var _ action_kit_sdk.Action[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStatus[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStop[StressActionState] = (*stressAction)(nil)

func newStressAction(
	runc runc.Runc,
	description func() action_kit_api.ActionDescription,
	optsProvider stressOptsProvider,
) action_kit_sdk.Action[StressActionState] {
	return &stressAction{
		description:  description(),
		optsProvider: optsProvider,
		runc:         runc,
		stresses:     syncmap.Map{},
	}
}

func (a *stressAction) NewEmptyState() StressActionState {
	return StressActionState{}
}

func (a *stressAction) Describe() action_kit_api.ActionDescription {
	return a.description
}

func (a *stressAction) Prepare(_ context.Context, state *StressActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}

	opts, err := a.optsProvider(request)
	if err != nil {
		return nil, err
	}

	state.ContainerId = containerId[0]
	state.StressOpts = opts
	state.ExecutionId = request.ExecutionId
	return nil, nil
}

func (a *stressAction) Start(_ context.Context, state *StressActionState) (*action_kit_api.StartResult, error) {
	s, err := stress.New(a.runc, RemovePrefix(state.ContainerId), state.StressOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	a.stresses.Store(state.ExecutionId, s)

	if err := s.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting stress container %s", state.ContainerId),
			},
		}),
	}, nil
}

func (a *stressAction) Status(_ context.Context, state *StressActionState) (*action_kit_api.StatusResult, error) {
	completed, err := a.isStressCompleted(state.ExecutionId)

	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: true,
			Error: &action_kit_api.ActionKitError{
				Status: extutil.Ptr(action_kit_api.Failed),
				Title:  fmt.Sprintf("Failed to stress container: %s", err),
			},
		}, nil
	}

	if completed {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Stessing container %s stopped", state.ContainerId),
				},
			},
		}, nil
	}

	return &action_kit_api.StatusResult{Completed: false}, nil
}

func (a *stressAction) Stop(_ context.Context, state *StressActionState) (*action_kit_api.StopResult, error) {
	var messages []action_kit_api.Message

	stopped := a.cancelStressContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Cancelled stress container %s", state.ContainerId),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *stressAction) isStressCompleted(executionId uuid.UUID) (bool, error) {
	s, ok := a.stresses.Load(executionId)
	if !ok {
		return true, nil
	}

	select {
	case err := <-s.(*stress.Stress).Wait():
		a.stresses.Delete(executionId)
		return true, err
	default:
		return false, nil
	}
}

func (a *stressAction) cancelStressContainer(executionId uuid.UUID) bool {
	s, ok := a.stresses.Load(executionId)
	if !ok {
		return false
	}

	s.(*stress.Stress).Stop()
	return true
}
