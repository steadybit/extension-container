// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/stress"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

func NewStressIoContainerAction(r runc.Runc) action_kit_sdk.Action[StressActionState] {
	return newStressAction(r, getStressIoDescription, stressIo)
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
		Label:       "Stress Container IO",
		Description: "Stresses memory in the container cgroup for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(stressIOIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.External,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "mode",
				Label:        "Mode",
				Description:  extutil.Ptr("How should the IO be stressed?"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr(string(ModeReadWriteAndFlush)),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
				MinValue:     extutil.Ptr(1),
				MaxValue:     extutil.Ptr(100),
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
				Description:  extutil.Ptr("How many workers should continually write, read and remove temporary files?"),
				Type:         action_kit_api.StressngWorkers,
				DefaultValue: extutil.Ptr("0"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(01),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should IO be stressed?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
			},
			{
				Name:         "path",
				Label:        "Path",
				Description:  extutil.Ptr("Path where the IO should be inflicted"),
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

func stressIo(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error) {
	workers := extutil.ToInt(request.Config["workers"])
	mode := extutil.ToString(request.Config["mode"])
	if mode == "" {
		mode = string(ModeReadWriteAndFlush)
	}

	opts := stress.StressOpts{
		TempPath: extutil.ToString(request.Config["path"]),
		Timeout:  time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}

	if mode == string(ModeReadWriteAndFlush) || mode == string(ModeReadWrite) {
		opts.HddWorkers = &workers
		opts.HddBytes = fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64)))
	}

	if mode == string(ModeReadWriteAndFlush) || mode == string(ModeFlush) {
		opts.IoWorkers = &workers
	}

	return opts, nil
}
