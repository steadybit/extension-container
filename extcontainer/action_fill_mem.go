// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/memfill"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
	"os/exec"
	"strings"
	"time"
)

type fillMemoryAction struct {
	runc     runc.Runc
	memfills syncmap.Map
}

type FillMemoryActionState struct {
	ExecutionId     uuid.UUID
	ContainerID     string
	TargetProcess   runc.LinuxProcessInfo
	FillMemoryOpts  memfill.Opts
	IgnoreExitCodes []int
}

// Make sure fillMemoryAction implements all required interfaces
var _ action_kit_sdk.Action[FillMemoryActionState] = (*fillMemoryAction)(nil)
var _ action_kit_sdk.ActionWithStop[FillMemoryActionState] = (*fillMemoryAction)(nil)
var _ action_kit_sdk.ActionWithStatus[FillMemoryActionState] = (*fillMemoryAction)(nil)

func NewFillMemoryContainerAction(r runc.Runc) action_kit_sdk.Action[FillMemoryActionState] {
	return &fillMemoryAction{
		runc: r,
	}
}

func (a *fillMemoryAction) NewEmptyState() FillMemoryActionState {
	return FillMemoryActionState{}
}

func (a *fillMemoryAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.fill_mem", BaseActionID),
		Label:       "Fill Memory",
		Description: "Fills the memory of the container for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(fillMemoryIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("Resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should the memory be filled?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			{
				Name:         "mode",
				Label:        "Mode",
				Description:  extutil.Ptr("How would you like to specify the amount of data to be filled?"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr(string(memfill.ModeUsage)),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "Fill and meet specified usage",
						Value: string(memfill.ModeUsage),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Fill the specified amount",
						Value: string(memfill.ModeAbsolute),
					},
				}),
			},
			{
				Name:         "size",
				Label:        "Size",
				Description:  extutil.Ptr("Depending on the unit, specify the percentage or the number of Megabytes to fill."),
				Type:         action_kit_api.Integer,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
			},
			{
				Name:         "unit",
				Label:        "Unit",
				Description:  extutil.Ptr("Unit for the size parameter."),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr(string(memfill.UnitPercent)),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(4),
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "Megabytes",
						Value: string(memfill.UnitMegabyte),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "% of total memory",
						Value: string(memfill.UnitPercent),
					},
				}),
			},
			{
				Name:         "failOnOomKill",
				Label:        "Fail on OOM Kill",
				Description:  extutil.Ptr("Should an OOM kill be considered a failure?"),
				Type:         action_kit_api.Boolean,
				DefaultValue: extutil.Ptr("false"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(5),
			},
		},
	}
}

func fillMemoryOpts(request action_kit_api.PrepareActionRequestBody) (memfill.Opts, error) {
	opts := memfill.Opts{
		BinaryPath: config.Config.MemfillPath,
		Size:       extutil.ToInt(request.Config["size"]),
		Mode:       memfill.Mode(request.Config["mode"].(string)),
		Unit:       memfill.Unit(request.Config["unit"].(string)),
		Duration:   time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}
	return opts, nil
}

func (a *fillMemoryAction) Prepare(ctx context.Context, state *FillMemoryActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}
	state.ContainerID = containerId[0]

	opts, err := fillMemoryOpts(request)
	if err != nil {
		return nil, err
	}

	processInfo, err := getProcessInfoForContainer(ctx, a.runc, RemovePrefix(state.ContainerID))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill memory settings.", err)
	}

	state.TargetProcess = processInfo
	state.FillMemoryOpts = opts
	state.ExecutionId = request.ExecutionId

	if !extutil.ToBool(request.Config["failOnOomKill"]) {
		state.IgnoreExitCodes = []int{137}
	}
	return nil, nil
}

func (a *fillMemoryAction) Start(_ context.Context, state *FillMemoryActionState) (*action_kit_api.StartResult, error) {
	copiedOpts := state.FillMemoryOpts
	memFill, err := memfill.New(state.TargetProcess, copiedOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to fill memory in container", err)
	}

	a.memfills.Store(state.ExecutionId, memFill)

	if err := memFill.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to fill memory in container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting fill memory in container %s with args %s", state.ContainerID, strings.Join(memFill.Args(), " ")),
			},
		}),
	}, nil
}

func (a *fillMemoryAction) Status(_ context.Context, state *FillMemoryActionState) (*action_kit_api.StatusResult, error) {
	exited, err := a.fillMemoryExited(state.ExecutionId)
	if !exited {
		return &action_kit_api.StatusResult{Completed: false}, nil
	}

	if err == nil {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("fill memory for container %s stopped", state.ContainerID),
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

		for _, ignore := range state.IgnoreExitCodes {
			if exitCode == ignore {
				return &action_kit_api.StatusResult{
					Completed: true,
					Messages: &[]action_kit_api.Message{
						{
							Level:   extutil.Ptr(action_kit_api.Warn),
							Message: fmt.Sprintf("memfill exited unexpectedly: %s", errMessage),
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
			Title:  fmt.Sprintf("Failed to fill memory for container: %s", errMessage),
		},
	}, nil
}

func (a *fillMemoryAction) Stop(_ context.Context, state *FillMemoryActionState) (*action_kit_api.StopResult, error) {
	messages := make([]action_kit_api.Message, 0)

	if a.stopFillMemoryContainer(state.ExecutionId) {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled fill memory in container %s", state.ContainerID),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *fillMemoryAction) fillMemoryExited(executionId uuid.UUID) (bool, error) {
	s, ok := a.memfills.Load(executionId)
	if !ok {
		return true, nil
	}
	return s.(*memfill.MemFill).Exited()
}

func (a *fillMemoryAction) stopFillMemoryContainer(executionId uuid.UUID) bool {
	s, ok := a.memfills.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	err := s.(*memfill.MemFill).Stop()
	return err == nil
}
