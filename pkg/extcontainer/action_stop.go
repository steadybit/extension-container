// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/types"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

type stopAction struct {
	client     types.Client
	completers map[uuid.UUID]*completer
}

type completer struct {
	err    <-chan error
	cancel context.CancelFunc
}

type StopActionState struct {
	ContainerId string
	Graceful    bool
	ExecutionId uuid.UUID
}

// Make sure stopAction implements all required interfaces
var _ action_kit_sdk.Action[StopActionState] = (*stopAction)(nil)
var _ action_kit_sdk.ActionWithStatus[StopActionState] = (*stopAction)(nil)
var _ action_kit_sdk.ActionWithStop[StopActionState] = (*stopAction)(nil)

func NewStopContainerAction(client types.Client) action_kit_sdk.Action[StopActionState] {
	return &stopAction{
		client:     client,
		completers: make(map[uuid.UUID]*completer),
	}
}

func (a *stopAction) NewEmptyState() StopActionState {
	return StopActionState{}
}

func (a *stopAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:                       fmt.Sprintf("%s.stop", targetID),
		Label:                    "Stop Container",
		Description:              "Stops or kills the Container",
		Version:                  extbuild.GetSemverVersionStringOrUnknown(),
		Icon:                     extutil.Ptr(targetIcon),
		TargetType:               extutil.Ptr(targetID),
		TargetSelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
			//TODO
		}),
		Category:    extutil.Ptr("state"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.Internal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "graceful",
				Label:        "Graceful",
				Description:  extutil.Ptr("Stopped the container gracefully using SIGTERM or immediately killed using the SIGKILL signal?"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr("true"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("1s"),
		}),
		Stop: extutil.Ptr(action_kit_api.MutatingEndpointReference{}),
	}
}

func (a *stopAction) Prepare(_ context.Context, state *StopActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	containerId := request.Target.Attributes["container.id"]
	if containerId == nil || len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}

	graceful := request.Config["graceful"]
	if graceful == nil {
		graceful = true
	}

	state.ContainerId = RemovePrefix(containerId[0])
	state.Graceful = graceful.(bool)
	state.ExecutionId = request.ExecutionId
	return nil, nil
}

func (a *stopAction) Start(_ context.Context, state *StopActionState) (*action_kit_api.StartResult, error) {
	err := a.stopContainer(state.ExecutionId, state.ContainerId, state.Graceful)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stop container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Stopping container %s (graceful=%t)", state.ContainerId, state.Graceful),
			},
		}),
	}, nil
}

func (a *stopAction) Status(_ context.Context, state *StopActionState) (*action_kit_api.StatusResult, error) {
	var messages []action_kit_api.Message
	completed, err := a.isStopContainerCompleted(state.ExecutionId)
	if err != nil {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Error),
			Message: fmt.Sprintf("Failed to stop container %s: %s", state.ContainerId, err),
		})
	} else if completed {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Container %s stopped", state.ContainerId),
		})
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages:  &messages,
	}, nil
}

func (a *stopAction) Stop(_ context.Context, state *StopActionState) (*action_kit_api.StopResult, error) {
	var messages []action_kit_api.Message

	stopped := a.cancelStopContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Cancelled stop container %s", state.ContainerId),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *stopAction) stopContainer(executionId uuid.UUID, containerId string, graceful bool) error {
	//When the stop actions are graceful, it may take some time until the container is actually stopped.
	//Therefore, we start the stop action, in a separate go routine, and return immediately.
	//We save the cancel function and the error channel in a map, so that the status action can check if the stop is still completable and could also cancel if requested
	errorChannel := make(chan error)
	stopCtx, stopCancel := context.WithCancel(context.Background())

	a.completers[executionId] = &completer{
		err:    errorChannel,
		cancel: stopCancel,
	}

	go func() {
		errorChannel <- a.client.Stop(stopCtx, containerId, graceful)
		close(errorChannel)
	}()

	select {
	case err := <-errorChannel:
		if err != nil {
			return err
		}
	case <-time.After(1 * time.Second):
		break
	}
	return nil
}

func (a *stopAction) isStopContainerCompleted(executionId uuid.UUID) (bool, error) {
	running, ok := a.completers[executionId]
	if !ok {
		return true, nil
	}

	select {
	case err := <-running.err:
		delete(a.completers, executionId)
		return true, err
	default:
		return false, nil
	}
}

func (a *stopAction) cancelStopContainer(executionId uuid.UUID) bool {
	running, ok := a.completers[executionId]
	if !ok {
		return false
	}

	running.cancel()
	<-running.err
	return true
}
