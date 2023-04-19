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
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/stress"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
)

type optsProvider func(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error)

type stressAction struct {
	description  action_kit_api.ActionDescription
	client       types.Client
	stresses     map[uuid.UUID]*stress.Stress
	optsProvider optsProvider
	runc         runc.Runc
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

func newStressContainerAction(
	client types.Client,
	runc runc.Runc,
	description func() action_kit_api.ActionDescription,
	optsProvider optsProvider,
) action_kit_sdk.Action[StressActionState] {
	return &stressAction{
		description:  description(),
		optsProvider: optsProvider,
		client:       client,
		runc:         runc,
		stresses:     make(map[uuid.UUID]*stress.Stress),
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
	if containerId == nil || len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}

	opts, err := a.optsProvider(request)
	if err != nil {
		return nil, err
	}

	state.ContainerId = RemovePrefix(containerId[0])
	state.StressOpts = opts
	return nil, nil
}

func (a *stressAction) Start(_ context.Context, state *StressActionState) (*action_kit_api.StartResult, error) {
	s, err := stress.New(a.runc, state.ContainerId, state.StressOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	a.stresses[state.ExecutionId] = s

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
	var messages []action_kit_api.Message
	completed, err := a.isStressCompleted(state.ExecutionId)
	if err != nil {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Error),
			Message: fmt.Sprintf("Failed to stress container %s: %s", state.ContainerId, err),
		})
	} else if completed {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Stessing container %s stopped", state.ContainerId),
		})
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages:  &messages,
	}, nil
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
	s, ok := a.stresses[executionId]
	if !ok {
		return true, nil
	}

	select {
	case err := <-s.Wait():
		delete(a.stresses, executionId)
		return true, err
	default:
		return false, nil
	}
}

func (a *stressAction) cancelStressContainer(executionId uuid.UUID) bool {
	s, ok := a.stresses[executionId]
	if !ok {
		return false
	}

	s.Stop()
	return true
}
