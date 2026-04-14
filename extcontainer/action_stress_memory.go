// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2025 Steadybit GmbH

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

func NewStressMemoryContainerAction(r ociruntime.OciRuntime, c types.Client) action_kit_sdk.Action[StressActionState] {
	return newStressAction(r, c, getStressMemoryDescription, stressMemory)
}

func getStressMemoryDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stress_mem", BaseActionID),
		Label:       "Stress Memory",
		Description: "Stresses memory in the container cgroup for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(stressMemoryIcon),
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
				Name:         "percentage",
				Label:        "Load on Container Memory",
				Description:  new("How much of the total container memory should be allocated?"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: new("80"),
				Required:     new(true),
				Order:        new(0),
				MinValue:     new(1),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long should container memory be wasted?"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("30s"),
				Required:     new(true),
				Order:        new(2),
			},
			{
				Name:         "failOnOomKill",
				Label:        "Fail on OOM Kill",
				Description:  new("Should an OOM kill be considered a failure?"),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: new("false"),
				Required:     new(true),
				Order:        new(3),
			},
		},
	}
}

func stressMemory(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error) {
	return stress.Opts{
		VmWorkers: new(1),
		VmBytes:   fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64))),
		VmHang:    0,
		Timeout:   time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}, nil
}
