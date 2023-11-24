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
				Order:        extutil.Ptr(2),
			},
			{
				Name:         "path",
				Label:        "TempPath",
				Description:  extutil.Ptr("TempPath where the file should be created"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr("/"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
			},
			{
				Name:         "percentage",
				Label:        "Disk Usage Percentage",
				Description:  extutil.Ptr("Percentage of available disk space to use"),
				Type:         action_kit_api.Percentage,
				DefaultValue: extutil.Ptr("50"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
				MinValue:     extutil.Ptr(1),
				MaxValue:     extutil.Ptr(100),
			},
		},
	}
}

func fillDisk(request action_kit_api.PrepareActionRequestBody) (diskfill.Opts, error) {
	opts := diskfill.Opts{
		TempPath: extutil.ToString(request.Config["path"]),
	}

	//opts.HddBytes = fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64)))
	opts.BlockSize = 256 * 1024   // 256 MB
	opts.SizeToFill = 10 * 1024 * 1024 // 1 GB
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

	opts, err := fillDisk(request)
	if err != nil {
		return nil, err
	}

	cfg, err := GetConfigForContainer(ctx, a.runc, RemovePrefix(containerId[0]))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare stress settings.", err)
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

	s, err := diskfill.New(ctx, a.runc, state.ContainerConfig, state.FillDiskOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to fill disk in container", err)
	}

	a.diskfills.Store(state.ExecutionId, s)

	if err := s.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to  fill disk in container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting fill disk in container %s with args %s", state.ContainerConfig.ContainerID, strings.Join(state.FillDiskOpts.DDArgs(), " ")),
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

func (a *fillDiskAction) stopFillDiskContainer(executionId uuid.UUID) bool {
	s, ok := a.diskfills.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	s.(*diskfill.DiskFill).Stop()
	return true
}
