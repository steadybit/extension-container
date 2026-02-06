// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
)

type stressOptsProvider func(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error)

type stressAction struct {
	ociRuntime   ociruntime.OciRuntime
	client       types.Client
	description  action_kit_api.ActionDescription
	optsProvider stressOptsProvider
	stresses     syncmap.Map
}

type StressActionState struct {
	Sidecar         stress.SidecarOpts
	ContainerID     string
	TargetLabel     string
	StressOpts      stress.Opts
	ExecutionId     uuid.UUID
	IgnoreExitCodes []int
}

// Make sure stressAction implements all required interfaces
var _ action_kit_sdk.Action[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStatus[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStop[StressActionState] = (*stressAction)(nil)

func newStressAction(
	r ociruntime.OciRuntime,
	client types.Client,
	description func() action_kit_api.ActionDescription,
	optsProvider stressOptsProvider,
) action_kit_sdk.Action[StressActionState] {
	return &stressAction{
		description:  description(),
		optsProvider: optsProvider,
		ociRuntime:   r,
		client:       client,
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
	container, label, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, extension_kit.ToError("Failed to get target container", err)
	}

	state.ContainerID = container.Id()
	state.TargetLabel = label

	processInfo, err := getProcessInfoForContainer(ctx, a.ociRuntime, RemovePrefix(state.ContainerID), specs.PIDNamespace, specs.CgroupNamespace)
	if err != nil {
		return nil, extension_kit.ToError("Failed to read target process info", err)
	}

	state.Sidecar = stress.SidecarOpts{
		TargetProcess: processInfo,
		IdSuffix:      RemovePrefix(state.ContainerID)[:8],
		ExecutionId:   request.ExecutionId,
	}

	opts, err := a.optsProvider(request)
	if err != nil {
		return nil, err
	}

	readAndAdaptToContainerLimits(ctx, processInfo, &opts)

	state.StressOpts = opts
	state.ExecutionId = request.ExecutionId
	if !extutil.ToBool(request.Config["failOnOomKill"]) {
		state.IgnoreExitCodes = []int{137}
	}
	return nil, nil
}

func (a *stressAction) Start(ctx context.Context, state *StressActionState) (*action_kit_api.StartResult, error) {
	s, err := stress.NewStressRunc(ctx, a.ociRuntime, state.Sidecar, state.StressOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stess container", err)
	}

	a.stresses.Store(state.ExecutionId, s)

	if err := s.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting stress container %s with args %s", state.TargetLabel, strings.Join(state.StressOpts.Args(), " ")),
			},
		}),
	}, nil
}

func (a *stressAction) Status(ctx context.Context, state *StressActionState) (*action_kit_api.StatusResult, error) {
	exited, err := a.stressExited(state.ExecutionId)
	if !exited {
		return &action_kit_api.StatusResult{Completed: false}, nil
	}

	if err == nil {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Stressing container %s stopped", state.TargetLabel),
				},
			},
		}, nil
	}

	errMessage := err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		if len(exitErr.Stderr) > 0 {
			errMessage = fmt.Sprintf("%s\n%s", exitErr.Error(), string(exitErr.Stderr))
		}

		// stress-ng was stopped by a signal, which is ok if the target process is also gone.
		if exitErr.ExitCode() == -1 {
			_, err := getProcessInfoForContainer(ctx, a.ociRuntime, RemovePrefix(state.ContainerID), specs.PIDNamespace)
			if err != nil {
				return &action_kit_api.StatusResult{
					Completed: true,
					Messages: &[]action_kit_api.Message{
						{
							Level:   extutil.Ptr(action_kit_api.Warn),
							Message: fmt.Sprintf("stress-ng exited unexpectedly, target container stopped: %s", errMessage),
						},
					},
					Summary: &action_kit_api.Summary{
						Level: action_kit_api.SummaryLevelWarning,
						Text:  "stress-ng exited unexpectedly, target container stopped",
					},
				}, nil
			}
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
					Summary: &action_kit_api.Summary{
						Level: action_kit_api.SummaryLevelWarning,
						Text:  "stress-ng exited unexpectedly",
					},
				}, nil
			}
		}
	}

	return &action_kit_api.StatusResult{
		Completed: true,
		Error: &action_kit_api.ActionKitError{
			Status: extutil.Ptr(action_kit_api.Errored),
			Title:  "Failed to stress container",
			Detail: extutil.Ptr(errMessage),
		},
	}, nil
}

func (a *stressAction) Stop(_ context.Context, state *StressActionState) (*action_kit_api.StopResult, error) {
	messages := make([]action_kit_api.Message, 0)

	if a.stopStressContainer(state.ExecutionId) {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled stress container %s", state.TargetLabel),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *stressAction) stressExited(executionId uuid.UUID) (bool, error) {
	s, ok := a.stresses.Load(executionId)
	if !ok {
		return true, nil
	}
	return s.(stress.Stress).Exited()
}

func (a *stressAction) stopStressContainer(executionId uuid.UUID) bool {
	s, ok := a.stresses.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	s.(stress.Stress).Stop()
	return true
}
