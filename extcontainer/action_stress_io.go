// Copyright 2025 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

func NewStressIoContainerAction(r ociruntime.OciRuntime, c types.Client) action_kit_sdk.Action[StressActionState] {
	return newStressAction(r, c, getStressIoDescription, stressIo)
}

type Mode string

const (
	ModeReadWriteAndFlush Mode = "read_write_and_flush"
	ModeReadWrite         Mode = "read_write"
	ModeFlush             Mode = "flush"
)

func getStressIoDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stress_io", BaseActionID),
		Label:       "Stress IO",
		Description: "Stresses IO in the container using read/write/flush operations for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(stressIOIcon),
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
				Name:         "mode",
				Label:        "Mode",
				Description:  new("How should the IO be stressed?"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: new(string(ModeReadWriteAndFlush)),
				Required:     new(true),
				Order:        new(0),
				MinValue:     new(1),
				MaxValue:     new(100),
				Options: &[]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "read/write and flush",
						Value: string(ModeReadWriteAndFlush),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "read/write only",
						Value: string(ModeReadWrite),
					},
					action_kit_api.ExplicitParameterOption{
						Label: "flush only",
						Value: string(ModeFlush),
					},
				},
			},
			{
				Name:         "workers",
				Label:        "Workers",
				Description:  new("How many workers should continually write, read and remove temporary files?"),
				Type:         action_kit_api.ActionParameterTypeStressngWorkers,
				DefaultValue: new("0"),
				Required:     new(true),
				Order:        new(01),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long should IO be stressed?"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("30s"),
				Required:     new(true),
				Order:        new(2),
			},
			{
				Name:         "path",
				Label:        "Path",
				Description:  new("Path where the IO should be inflicted"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: new("/"),
				Required:     new(true),
				Order:        new(3),
			},
			{
				Name:         "mbytes_per_worker",
				Label:        "MBytes to write",
				Description:  new("How many megabytes should be written per stress operation?"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: new("1024"),
				Required:     new(true),
				Order:        new(3),
				MinValue:     new(1),
			},
		},
	}
}

func stressIo(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error) {
	workers := extutil.ToInt(request.Config["workers"])
	mode := extutil.ToString(request.Config["mode"])
	if mode == "" {
		mode = string(ModeReadWriteAndFlush)
	}

	opts := stress.Opts{
		TempPath: extutil.ToString(request.Config["path"]),
		Timeout:  time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}

	if mode == string(ModeReadWriteAndFlush) || mode == string(ModeReadWrite) {
		opts.HddWorkers = &workers
		opts.HddBytes = fmt.Sprintf("%dm", extutil.ToInt64(request.Config["mbytes_per_worker"]))
	}

	if mode == string(ModeReadWriteAndFlush) || mode == string(ModeFlush) {
		opts.IomixWorkers = &workers
	}

	return opts, nil
}
