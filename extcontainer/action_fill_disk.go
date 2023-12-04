// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/diskfill"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
	"runtime/trace"
	"strings"
)

var ID = fmt.Sprintf("%s.fill_disk", BaseActionID)

type fillDiskAction struct {
	client    types.Client
	runc      runc.Runc
	diskfills syncmap.Map
}

type FillDiskActionState struct {
	ExecutionId  uuid.UUID
	ContainerID  string
	Sidecar      diskfill.SidecarOpts
	FillDiskOpts diskfill.Opts
}

// Make sure fillDiskAction implements all required interfaces
var _ action_kit_sdk.Action[FillDiskActionState] = (*fillDiskAction)(nil)
var _ action_kit_sdk.ActionWithStop[FillDiskActionState] = (*fillDiskAction)(nil)

func NewFillDiskContainerAction(client types.Client, r runc.Runc) action_kit_sdk.Action[FillDiskActionState] {
	return &fillDiskAction{
		client: client,
		runc:   r,
	}
}

func (a *fillDiskAction) NewEmptyState() FillDiskActionState {
	return FillDiskActionState{}
}

func (a *fillDiskAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          ID,
		Label:       "Fill Disk",
		Description: "Fills the disk in the container for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(fillDiskIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should the disk be filled?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			{
				Name:         "mode",
				Label:        "Mode",
				Description:  extutil.Ptr("How would you like to specify the amount of data to be filled?"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
				DefaultValue: extutil.Ptr("PERCENTAGE"),
				Type:         action_kit_api.String,
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "Overall percentage of filled disk space in percent",
						Value: "PERCENTAGE",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Megabytes to write",
						Value: "MB_TO_FILL",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Megabytes to leave free on disk",
						Value: "MB_LEFT",
					},
				}),
			},
			{
				Name:         "size",
				Label:        "Fill Value (depending on Mode)",
				Description:  extutil.Ptr("Depending on the mode, specify the percentage of filled disk space or the number of Megabytes to be written or left free."),
				Type:         action_kit_api.Integer,
				DefaultValue: extutil.Ptr("80"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
			},
			{
				Name:         "path",
				Label:        "File Destination",
				Description:  extutil.Ptr("Where to temporarily write the file for filling the disk. It will be cleaned up afterwards."),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr("/tmp"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(4),
			},
			{
				Name:         "method",
				Label:        "Method used to fill disk",
				Description:  extutil.Ptr("Should the disk filled at once or over time?"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(5),
				DefaultValue: extutil.Ptr("AT_ONCE"),
				Type:         action_kit_api.String,
				Advanced:     extutil.Ptr(true),
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "At once (fallocate)",
						Value: "AT_ONCE",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Over time (dd)",
						Value: "OVER_TIME",
					},
				}),
			},
			{
				Name:         "blocksize",
				Label:        "Block Size (in MBytes) of the File to Write for method `At Once`",
				Description:  extutil.Ptr("Define the block size for writing the file with the dd command. If the block size is larger than the fill value, the fill value will be used as block size."),
				Type:         action_kit_api.Integer,
				DefaultValue: extutil.Ptr("5"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(6),
				MinValue:     extutil.Ptr(1),
				MaxValue:     extutil.Ptr(1024),
				Advanced:     extutil.Ptr(true),
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
	case "PERCENTAGE":
		opts.Mode = "PERCENTAGE"
	case "MB_TO_FILL":
		opts.Mode = "MB_TO_FILL"
	case "MB_LEFT":
		opts.Mode = "MB_LEFT"
	default:
		return opts, fmt.Errorf("invalid mode %s", request.Config["mode"])
	}

	switch request.Config["method"] {
	case "OVER_TIME":
		opts.Method = "OVER_TIME"
	case "AT_ONCE":
		opts.Method = "AT_ONCE"
	default:
		return opts, fmt.Errorf("invalid method %s", request.Config["method"])
	}

	return opts, nil
}

func (a *fillDiskAction) Prepare(ctx context.Context, state *FillDiskActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	ctx, task := trace.NewTask(ctx, "action_fill_disk.Prepare")
	defer task.End()
	trace.Log(ctx, "actionId", ID)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}
	state.ContainerID = containerId[0]

	opts, err := fillDiskOpts(request)
	if err != nil {
		return nil, err
	}

	processInfo, err := getProcessInforForContainer(ctx, a.runc, RemovePrefix(state.ContainerID))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill disk settings.", err)
	}

	state.Sidecar = diskfill.SidecarOpts{
		TargetProcess: processInfo,
		ImagePath:     "/",
		IdSuffix:      RemovePrefix(state.ContainerID)[:8],
	}
	state.FillDiskOpts = opts
	state.ExecutionId = request.ExecutionId
	return nil, nil
}

func (a *fillDiskAction) Start(ctx context.Context, state *FillDiskActionState) (*action_kit_api.StartResult, error) {
	ctx, task := trace.NewTask(ctx, "action_fill_disk.Start")
	defer task.End()
	trace.Log(ctx, "actionId", ID)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	copiedOpts := state.FillDiskOpts
	diskFill, err := diskfill.New(ctx, a.runc, state.Sidecar, copiedOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill disk in container", err)
	}

	a.diskfills.Store(state.ExecutionId, diskFill)

	if err := diskFill.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to  fill disk in container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting fill disk in container %s with args %s", state.ContainerID, strings.Join(diskFill.Args(), " ")),
			},
		}),
	}, nil
}

func (a *fillDiskAction) Stop(ctx context.Context, state *FillDiskActionState) (*action_kit_api.StopResult, error) {
	ctx, task := trace.NewTask(ctx, "action_fill_disk.Stop")
	defer task.End()
	trace.Log(ctx, "actionId", ID)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	messages := make([]action_kit_api.Message, 0)

	stopped := a.stopFillDiskContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled fill disk in container %s", state.ContainerID),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *fillDiskAction) fillDiskExited(executionId uuid.UUID) (bool, error) {
	s, ok := a.diskfills.Load(executionId)
	if !ok {
		return true, nil
	}
	return s.(*diskfill.DiskFill).Exited()
}

func (a *fillDiskAction) stopFillDiskContainer(executionId uuid.UUID) bool {
	s, ok := a.diskfills.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	err := s.(*diskfill.DiskFill).Stop()
	return err == nil
}
