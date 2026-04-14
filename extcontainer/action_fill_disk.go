// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/diskfill"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
)

type fillDiskAction struct {
	ociRuntime ociruntime.OciRuntime
	client     types.Client
	diskfills  syncmap.Map
}

type FillDiskActionState struct {
	ExecutionId     uuid.UUID
	ContainerID     string
	TargetLabel     string
	Sidecar         diskfill.SidecarOpts
	FillDiskOpts    diskfill.Opts
	IgnoreExitCodes []int
}

// Make sure fillDiskAction implements all required interfaces
var _ action_kit_sdk.Action[FillDiskActionState] = (*fillDiskAction)(nil)
var _ action_kit_sdk.ActionWithStop[FillDiskActionState] = (*fillDiskAction)(nil)
var _ action_kit_sdk.ActionWithStatus[FillDiskActionState] = (*fillDiskAction)(nil)

func NewFillDiskContainerAction(r ociruntime.OciRuntime, c types.Client) action_kit_sdk.Action[FillDiskActionState] {
	return &fillDiskAction{
		ociRuntime: r,
		client:     c,
	}
}

func (a *fillDiskAction) NewEmptyState() FillDiskActionState {
	return FillDiskActionState{}
}

func (a *fillDiskAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.fill_disk", BaseActionID),
		Label:       "Fill Disk",
		Description: "Fills the disk of the container for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(fillDiskIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  new("Container"),
		Category:    new("Resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long should the disk be filled?"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("30s"),
				Required:     new(true),
				Order:        new(1),
			},
			{
				Name:         "mode",
				Label:        "Mode",
				Description:  new("Specify how to calculate the amount of disk space to fill."),
				Required:     new(true),
				Order:        new(2),
				DefaultValue: extutil.Ptr(string(diskfill.MBToFill)),
				Type:         action_kit_api.ActionParameterTypeString,
				Options: new([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "Fill up to specified usage (in %)",
						Value: string(diskfill.Percentage),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Megabytes to write",
						Value: string(diskfill.MBToFill),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Megabytes to leave free on disk",
						Value: string(diskfill.MBLeft),
					},
				}),
			},
			{
				Name:         "size",
				Label:        "Fill Value (depending on Mode)",
				Description:  new("Depending on the mode, specify the percentage or megabytes to use."),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: new("500"),
				Required:     new(true),
				Order:        new(3),
			},
			{
				Name:         "path",
				Label:        "File Destination",
				Description:  new("Where to temporarily write the file for filling the disk. It will be cleaned up afterwards."),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: new("/tmp"),
				Required:     new(true),
				Order:        new(4),
			},
			{
				Name:         "method",
				Label:        "Method used to fill disk",
				Description:  new("Should the disk filled at once or over time?"),
				Required:     new(true),
				Order:        new(5),
				DefaultValue: new("AT_ONCE"),
				Type:         action_kit_api.ActionParameterTypeString,
				Advanced:     new(true),
				Options: new([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "At once (fallocate)",
						Value: string(diskfill.AtOnce),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Over time (dd)",
						Value: string(diskfill.OverTime),
					},
				}),
			},
			{
				Name:         "blocksize",
				Label:        "Block Size (in MBytes) of the File to Write for method `Over Time`",
				Description:  new("Define the block size for writing the file with the dd command. If the block size is larger than the fill value, the fill value will be used as block size."),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: new("5"),
				Required:     new(true),
				Order:        new(6),
				MinValue:     new(1),
				MaxValue:     new(1024),
				Advanced:     new(true),
			},
			{
				Name:         "failOnOomKill",
				Label:        "Fail on OOM Kill",
				Description:  new("Should an OOM kill be considered a failure?"),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: new("true"),
				Required:     new(false),
				Advanced:     new(true),
			},
		},
	}
}

func fillDiskOpts(request action_kit_api.PrepareActionRequestBody) (diskfill.Opts, error) {
	opts := diskfill.Opts{
		TempPath: extutil.ToString(request.Config["path"]),
	}

	opts.BlockSize = int(request.Config["blocksize"].(float64))
	opts.Size = int(request.Config["size"].(float64))
	switch request.Config["mode"] {
	case string(diskfill.Percentage):
		opts.Mode = diskfill.Percentage
	case string(diskfill.MBToFill):
		opts.Mode = diskfill.MBToFill
	case string(diskfill.MBLeft):
		opts.Mode = diskfill.MBLeft
	default:
		return opts, fmt.Errorf("invalid mode %s", request.Config["mode"])
	}

	switch request.Config["method"] {
	case string(diskfill.OverTime):
		opts.Method = diskfill.OverTime
	case string(diskfill.AtOnce):
		opts.Method = diskfill.AtOnce
	default:
		return opts, fmt.Errorf("invalid method %s", request.Config["method"])
	}

	return opts, nil
}

func (a *fillDiskAction) Prepare(ctx context.Context, state *FillDiskActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	container, label, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, extension_kit.ToError("Failed to get target container", err)
	}

	state.ContainerID = container.Id()
	state.TargetLabel = label

	opts, err := fillDiskOpts(request)
	if err != nil {
		return nil, err
	}

	processInfo, err := getProcessInfoForContainer(ctx, a.ociRuntime, RemovePrefix(state.ContainerID), specs.PIDNamespace)
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill disk settings.", err)
	}

	state.Sidecar = diskfill.SidecarOpts{
		TargetProcess: processInfo,
		Id:            fmt.Sprintf("%s-%s", request.ExecutionId.String()[24:], RemovePrefix(state.ContainerID)[:8]),
	}
	state.FillDiskOpts = opts
	state.ExecutionId = request.ExecutionId

	if err := diskfill.CheckPathWritableRunc(ctx, a.ociRuntime, state.Sidecar, opts.TempPath); err != nil {
		return nil, extension_kit.ToError(err.Error(), nil)
	}

	if !extutil.ToBool(request.Config["failOnOomKill"]) {
		state.IgnoreExitCodes = []int{137}
	}
	return nil, nil
}

func (a *fillDiskAction) Start(ctx context.Context, state *FillDiskActionState) (*action_kit_api.StartResult, error) {
	copiedOpts := state.FillDiskOpts
	diskFill, err := diskfill.NewDiskfillRunc(ctx, a.ociRuntime, state.Sidecar, copiedOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to fill disk in container", err)
	}

	a.diskfills.Store(state.ExecutionId, diskFill)

	if err := diskFill.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to fill disk in container", err)
	}

	messages := []action_kit_api.Message{
		{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Starting fill disk in container %s with args %s", state.TargetLabel, strings.Join(diskFill.Args(), " ")),
		},
	}

	if diskFill.Noop() {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: "Noop mode is enabled. No disk will be filled, because the disk is already filled enough.",
		})
	}

	return &action_kit_api.StartResult{
		Messages: new(messages),
	}, nil
}

func (a *fillDiskAction) Status(ctx context.Context, state *FillDiskActionState) (*action_kit_api.StatusResult, error) {
	_, err := a.fillDiskContainerExited(state.ExecutionId)
	if err == nil {
		return &action_kit_api.StatusResult{Completed: false}, nil
	}

	errMessage := err.Error()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		log.Debug().Err(err).Msgf("fill disk exited unexpectedly with exit code %d", exitCode)

		if len(exitErr.Stderr) > 0 {
			errMessage = fmt.Sprintf("%s\n%s", exitErr.Error(), string(exitErr.Stderr))
		}

		// fill disk was stopped by a signal, which is ok if the target process is also gone.
		if exitErr.ExitCode() == -1 {
			_, err := getProcessInfoForContainer(ctx, a.ociRuntime, RemovePrefix(state.ContainerID), specs.PIDNamespace)
			if err != nil {
				return &action_kit_api.StatusResult{
					Completed: true,
					Messages: &[]action_kit_api.Message{
						{
							Level:   extutil.Ptr(action_kit_api.Warn),
							Message: fmt.Sprintf("fill disk exited unexpectedly, target container stopped: %s", errMessage),
						},
					},
					Summary: &action_kit_api.Summary{
						Level: action_kit_api.SummaryLevelWarning,
						Text:  "fill disk exited unexpectedly, target container stopped",
					},
				}, nil
			}
		}

		if slices.Contains(state.IgnoreExitCodes, exitCode) {
			return &action_kit_api.StatusResult{
				Completed: true,
				Messages: &[]action_kit_api.Message{
					{
						Level:   extutil.Ptr(action_kit_api.Warn),
						Message: fmt.Sprintf("fill disk exited unexpectedly: %s", errMessage),
					},
				},
				Summary: &action_kit_api.Summary{
					Level: action_kit_api.SummaryLevelWarning,
					Text:  "fill disk exited unexpectedly",
				},
			}, nil
		}
	}

	return &action_kit_api.StatusResult{
		Completed: true,
		Error: &action_kit_api.ActionKitError{
			Status: extutil.Ptr(action_kit_api.Errored),
			Title:  "Failed to fill disk on container",
			Detail: new(errMessage),
		},
	}, nil
}

func (a *fillDiskAction) Stop(_ context.Context, state *FillDiskActionState) (*action_kit_api.StopResult, error) {
	if err := a.stopFillDiskContainer(state.ExecutionId); err != nil {
		return nil, extension_kit.ToError("Failed to stop fill disk on container", err)
	}

	return &action_kit_api.StopResult{
		Messages: &[]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: "Canceled fill disk on container",
			},
		},
	}, nil
}

func (a *fillDiskAction) fillDiskContainerExited(executionId uuid.UUID) (bool, error) {
	s, ok := a.diskfills.Load(executionId)
	if !ok {
		return true, nil
	}
	return s.(diskfill.Diskfill).Exited()
}

func (a *fillDiskAction) stopFillDiskContainer(executionId uuid.UUID) error {
	s, ok := a.diskfills.LoadAndDelete(executionId)
	if !ok {
		return errors.New("no diskfill container found")
	}
	return s.(diskfill.Diskfill).Stop()
}
