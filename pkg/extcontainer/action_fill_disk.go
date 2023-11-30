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
	"github.com/steadybit/extension-container/pkg/diskfill"
	"github.com/steadybit/extension-container/pkg/utils"
	extension_kit "github.com/steadybit/extension-kit"
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
	ContainerId     string
	ExecutionId     uuid.UUID
	ContainerConfig utils.TargetContainerConfig
	FillDiskOpts    diskfill.Opts
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
						Label: "Overall percentage of filled disk space in percent. Greater than 100% will fill the disk completely.",
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
				Name:         "blocksize",
				Label:        "Block Size (in MBytes) of the File to Write",
				Description:  extutil.Ptr("Define the block size for writing the file. Larger block sizes increase the performance. If the block size is larger than the fill value, the fill value will be used as block size."),
				Type:         action_kit_api.Integer,
				DefaultValue: extutil.Ptr(fmt.Sprintf("%d", diskfill.MaxBlockSize)),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(5),
				MinValue: 	 extutil.Ptr(1),
				MaxValue: 	 extutil.Ptr(1024),
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
		return opts, fmt.Errorf("invalid unit %s", request.Config["mode"])
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

	opts, err := fillDiskOpts(request)
	if err != nil {
		return nil, err
	}

	cfg, err := GetConfigForContainer(ctx, a.runc, RemovePrefix(containerId[0]))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill disk settings.", err)
	}

	state.ContainerConfig = cfg
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
	diskFill, err := diskfill.New(ctx, a.runc, state.ContainerConfig, copiedOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare fill disk in container", err)
	}

	a.diskfills.Store(state.ExecutionId, diskFill)

	if !diskFill.HasSomethingToDo() {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Nothing to do for fill disk in container %s", state.ContainerConfig.ContainerID),
				},
			}),
		}, nil
	}

	if err := diskFill.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to  fill disk in container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting fill disk in container %s with args %s", state.ContainerConfig.ContainerID, strings.Join(diskFill.Args(), " ")),
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

	copiedOpts := state.FillDiskOpts
	stopped := a.stopFillDiskContainer(ctx, state.ExecutionId, a.runc, state.ContainerConfig, copiedOpts)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled fill disk in container %s", state.ContainerConfig.ContainerID),
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

func (a *fillDiskAction) stopFillDiskContainer(ctx context.Context, executionId uuid.UUID, r runc.Runc, config utils.TargetContainerConfig, opts diskfill.Opts) bool {
	s, ok := a.diskfills.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	err := s.(*diskfill.DiskFill).Stop(ctx, r, config, opts)
	return err == nil
}
