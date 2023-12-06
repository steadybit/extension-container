// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

func NewStressMemoryContainerAction(r runc.Runc) action_kit_sdk.Action[StressActionState] {
	return newStressAction(r, getStressMemoryDescription, stressMemory)
}

func getStressMemoryDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stress_mem", BaseActionID),
		Label:       "Stress Container Memory",
		Description: "Stresses memory in the container cgroup for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(stressMemoryIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "percentage",
				Label:        "Load on Container Memory",
				Description:  extutil.Ptr("How much of the total memory should be allocated?"),
				Type:         action_kit_api.Percentage,
				DefaultValue: extutil.Ptr("200"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
				MinValue:     extutil.Ptr(1),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should memory be wasted?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
			},
			{
				Name:         "failOnOomKill",
				Label:        "Fail on OOM Kill",
				Description:  extutil.Ptr("Should an OOM kill be considered a failure?"),
				Type:         action_kit_api.Boolean,
				DefaultValue: extutil.Ptr("false"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
			},
		},
	}
}

func stressMemory(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error) {
	return stress.Opts{
		VmWorkers: extutil.Ptr(1),
		VmBytes:   fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64))),
		VmHang:    0,
		Timeout:   time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}, nil
}
