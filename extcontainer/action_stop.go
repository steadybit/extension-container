// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
	"time"
)

type stopAction struct {
	client     types.Client
	completers syncmap.Map //map[uuid.UUID]*completer
}

type completer struct {
	err    <-chan error
	cancel context.CancelFunc
}

type StopActionState struct {
	ContainerId string
	TargetLabel string
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
		completers: syncmap.Map{},
	}
}

func (a *stopAction) NewEmptyState() StopActionState {
	return StopActionState{}
}

func (a *stopAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stop", BaseActionID),
		Label:       "Stop Container",
		Description: "Stops or kills the Container",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(stopIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("State"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlInternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "graceful",
				Label:        "Graceful",
				Description:  extutil.Ptr("Stopped the container gracefully using SIGTERM or immediately killed using the SIGKILL signal?"),
				Type:         action_kit_api.Boolean,
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

func (a *stopAction) Prepare(ctx context.Context, state *StopActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	container, label, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, extension_kit.ToError("Failed to get target container", err)
	}

	state.ContainerId = container.Id()
	state.TargetLabel = label

	state.Graceful = extutil.ToBool(request.Config["graceful"])
	state.ExecutionId = request.ExecutionId
	return nil, nil
}

func (a *stopAction) Start(_ context.Context, state *StopActionState) (*action_kit_api.StartResult, error) {
	err := a.stopContainer(state.ExecutionId, RemovePrefix(state.ContainerId), state.Graceful)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stop container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Stopping container %s (graceful=%t)", state.TargetLabel, state.Graceful),
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
			Message: fmt.Sprintf("Failed to stop container %s: %s", state.TargetLabel, err),
		})
	} else if completed {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Container %s stopped", state.TargetLabel),
		})
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages:  &messages,
	}, nil
}

func (a *stopAction) Stop(_ context.Context, state *StopActionState) (*action_kit_api.StopResult, error) {
	messages := make([]action_kit_api.Message, 0)

	stopped := a.cancelStopContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled stop container %s", state.TargetLabel),
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

	a.completers.Store(executionId, &completer{
		err:    errorChannel,
		cancel: stopCancel,
	})
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
	running, ok := a.completers.Load(executionId)
	if !ok {
		return true, nil
	}

	select {
	case err := <-running.(*completer).err:
		a.completers.Delete(executionId)
		return true, err
	default:
		return false, nil
	}
}

func (a *stopAction) cancelStopContainer(executionId uuid.UUID) bool {
	running, ok := a.completers.Load(executionId)
	if !ok {
		return false
	}

	running.(*completer).cancel()
	<-running.(*completer).err
	return true
}
