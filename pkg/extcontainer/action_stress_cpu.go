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

func NewStressCpuContainerAction(client types.Client, runc runc.Runc) action_kit_sdk.Action[StressActionState] {
	return newStressContainerAction(client, runc, getStressCpuDescription, stressCpu)
}

func getStressCpuDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:                       fmt.Sprintf("%s.stress-cpu", targetID),
		Label:                    "Stress Container CPU",
		Description:              "Stresses CPU in the container cgroup for the given duration.",
		Version:                  extbuild.GetSemverVersionStringOrUnknown(),
		Icon:                     extutil.Ptr(targetIcon),
		TargetType:               extutil.Ptr(targetID),
		TargetSelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
			//TODO
		}),
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.External,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "cpuLoad",
				Label:        "Load on Container CPU",
				Description:  extutil.Ptr("How much CPU load should be inflicted?"),
				Type:         action_kit_api.Percentage,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
			},
			{
				Name:         "workers",
				Label:        "Container CPUs",
				Description:  extutil.Ptr("How many workers should be used to stress the CPU?"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr("0"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should the CPU be stressed?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
			},
		},
	}
}

func stressCpu(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error) {
	return stress.StressOpts{
		CpuWorkers: extutil.Ptr(int(request.Config["workers"].(float64))),
		CpuLoad:    int(request.Config["cpuLoad"].(float64)),
		Timeout:    time.Duration(int(request.Config["duration"].(float64))) * time.Millisecond,
	}, nil
}
