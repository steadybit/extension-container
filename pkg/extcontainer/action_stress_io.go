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

func NewStressIoContainerAction(client types.Client, runc runc.Runc) action_kit_sdk.Action[StressActionState] {
	return newStressContainerAction(client, runc, getStressIoDescription, stressIo)
}

func getStressIoDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:                       fmt.Sprintf("%s.stress-io", targetID),
		Label:                    "Stress Container IO",
		Description:              "Stresses memory in the container cgroup for the given duration.",
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
				Name:         "workers",
				Label:        "Workers",
				Description:  extutil.Ptr("How many workers should continually write, read and remove temporary files?"),
				Type:         action_kit_api.Integer,
				DefaultValue: extutil.Ptr("0"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
			},
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should IO be stressed?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			{
				Name:         "path",
				Label:        "Path",
				Description:  extutil.Ptr("Path where the IO should be inflicted"),
				Type:         action_kit_api.String,
				DefaultValue: extutil.Ptr("/"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(2),
			},
			{
				Name:         "percentage",
				Label:        "Disk Usage Percentage",
				Description:  extutil.Ptr("Percentage of available disk space to use"),
				Type:         action_kit_api.Percentage,
				DefaultValue: extutil.Ptr("50"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(3),
			},
		},
	}
}

func stressIo(request action_kit_api.PrepareActionRequestBody) (stress.StressOpts, error) {
	return stress.StressOpts{
		HddWorkers: extutil.Ptr(int(request.Config["workers"].(float64))),
		HddBytes:   fmt.Sprintf("%d%%", int(request.Config["percentage"].(float64))),
		IoWorkers:  extutil.Ptr(int(request.Config["workers"].(float64))),
		TempPath:   request.Config["path"].(string),
		Timeout:    time.Duration(int(request.Config["duration"].(float64))) * time.Millisecond,
	}, nil
}
