// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/stress"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"time"
)

func NewStressMemoryContainerAction(client types.Client, runc runc.Runc) action_kit_sdk.Action[StressActionState] {
	return newStressAction(client, runc, getStressMemoryDescription, stressMemory)
}

func getStressMemoryDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.stress_mem", targetID),
		Label:       "Stress Container Memory",
		Description: "Stresses memory in the container cgroup for the given duration.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(targetIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.External,
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
				MaxValue:     extutil.Ptr(100),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should memory be wasted?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
		},
	}
}

func stressMemory(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error) {
	return stress.StressOpts{
		VmWorkers: extutil.Ptr(1),
		VmBytes:   fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64))),
		VmHang:    0,
		Timeout:   time.Duration(extutil.ToInt64(request.Config["duration"])) * time.Millisecond,
	}, nil
}
