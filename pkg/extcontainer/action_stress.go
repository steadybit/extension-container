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
	"github.com/steadybit/extension-container/pkg/utils"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
	"os/exec"
	"runtime/trace"
	"strings"
)

type stressOptsProvider func(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error)

type stressAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider stressOptsProvider
	stresses     syncmap.Map
}

type StressActionState struct {
	ContainerConfig utils.TargetContainerConfig
	StressOpts      stress.Opts
	ExecutionId     uuid.UUID
	IgnoreExitCodes []int
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

func (a *stressAction) Prepare(ctx context.Context, state *StressActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Prepare")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}

	opts, err := a.optsProvider(request)
	if err != nil {
		return nil, err
	}

	cfg, err := GetConfigForContainer(ctx, a.runc, RemovePrefix(containerId[0]))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare stress settings.", err)
	}

	state.ContainerConfig = cfg
	state.StressOpts = opts
	state.ExecutionId = request.ExecutionId
	if !extutil.ToBool(request.Config["failOnOomKill"]) {
		state.IgnoreExitCodes = []int{137}
	}
	return nil, nil
}

func (a *stressAction) Start(ctx context.Context, state *StressActionState) (*action_kit_api.StartResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Start")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	s, err := stress.New(ctx, a.runc, state.ContainerConfig, state.StressOpts)
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
				Message: fmt.Sprintf("Starting stress container %s with args %s", state.ContainerConfig.ContainerID, strings.Join(state.StressOpts.Args(), " ")),
			},
		}),
	}, nil
}

func (a *stressAction) Status(ctx context.Context, state *StressActionState) (*action_kit_api.StatusResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Status")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	completed, err := a.isStressCompleted(state.ExecutionId)
	if err != nil {
		errMessage := err.Error()

		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			if len(exitErr.Stderr) > 0 {
				errMessage = fmt.Sprintf("%s\n%s", exitErr.Error(), string(exitErr.Stderr))
			}

			for _, ignore := range state.IgnoreExitCodes {
				if exitCode == ignore {
					return &action_kit_api.StatusResult{
						Completed: true,
						Messages: &[]action_kit_api.Message{
							{
								Level:   extutil.Ptr(action_kit_api.Warn),
								Message: fmt.Sprintf("stress-ng exited unexpectedly: %s", errMessage),
							},
						},
					}, nil
				}
			}
		}

		return &action_kit_api.StatusResult{
			Completed: true,
			Error: &action_kit_api.ActionKitError{
				Status: extutil.Ptr(action_kit_api.Failed),
				Title:  fmt.Sprintf("Failed to stress container: %s", errMessage),
			},
		}, nil
	}

	if completed {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Stessing container %s stopped", state.ContainerConfig.ContainerID),
				},
			},
		}, nil
	}

	return &action_kit_api.StatusResult{Completed: false}, nil
}

func (a *stressAction) Stop(ctx context.Context, state *StressActionState) (*action_kit_api.StopResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Stop")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	messages := make([]action_kit_api.Message, 0)

	stopped := a.stopStressContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled stress container %s", state.ContainerConfig.ContainerID),
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
		return true, err
	default:
		return false, nil
	}
}

func (a *stressAction) stopStressContainer(executionId uuid.UUID) bool {
	s, ok := a.stresses.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	s.(*stress.Stress).Stop()
	return true
}
