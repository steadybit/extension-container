// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

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

func NewStressCpuContainerAction(r ociruntime.OciRuntime, c types.Client) action_kit_sdk.Action[StressActionState] {
	return newStressAction(r, c, getStressCpuDescription, stressCpu)
}

func getStressCpuDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stress_cpu", BaseActionID),
		Label:       "Stress CPU",
		Description: "Stresses CPU in the container cgroup for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(stressCPUIcon),
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
				Name:         "cpuLoad",
				Label:        "Load on Container CPU",
				Description:  new("How much CPU load should be inflicted?"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: new("80"),
				Required:     new(true),
				Order:        new(0),
				MinValue:     new(1),
				MaxValue:     new(100),
			},
			{
				Name:         "workers",
				Label:        "Container CPUs",
				Description:  new("How many workers should be used to stress the CPU?"),
				Type:         action_kit_api.ActionParameterTypeStressngWorkers,
				DefaultValue: new("0"),
				Required:     new(true),
				Order:        new(1),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long should the CPU be stressed?"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("30s"),
				Required:     new(true),
				Order:        new(2),
			},
		},
	}
}

func stressCpu(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error) {
	return stress.Opts{
		CpuWorkers: new(extutil.ToInt(request.Config["workers"])),
		CpuLoad:    extutil.ToInt(request.Config["cpuLoad"]),
		Timeout:    time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}, nil
}
