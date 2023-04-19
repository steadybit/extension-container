// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/network"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

func NewNetworkBlackholeContainerAction(runc runc.Runc) action_kit_sdk.Action[NetworkActionState] {
	return &networkAction{
		optsProvider: blackhole(runc),
		description:  getNetworkBlackholeDescription(),
		runc:         runc,
	}
}

func getNetworkBlackholeDescription() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_blackhole", targetID),
		Label:       "Container Block Traffic",
		Description: "Blocks network traffic (incoming and outgoing).",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(targetIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Category:    extutil.Ptr("network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.External,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long should the traffic be blocked?"),
				Type:         action_kit_api.Duration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(0),
			},
			{
				Name:         "failOnHostNetwork",
				Label:        "Fail on Host Network",
				Description:  extutil.Ptr("Should the action fail if the container is using host network?"),
				Type:         action_kit_api.Boolean,
				DefaultValue: extutil.Ptr("true"),
				Required:     extutil.Ptr(true),
				Order:        extutil.Ptr(1),
			},
			{
				Name:         "hostname",
				Label:        "Hostname",
				Description:  extutil.Ptr("Restrict to/from which hosts the traffic is affected."),
				Type:         action_kit_api.StringArray,
				DefaultValue: extutil.Ptr(""),
				Advanced:     extutil.Ptr(true),
				Order:        extutil.Ptr(100),
			},
			{
				Name:         "ip",
				Label:        "IP Address",
				Description:  extutil.Ptr("Restrict to/from which IP addresses the traffic is affected."),
				Type:         action_kit_api.StringArray,
				DefaultValue: extutil.Ptr(""),
				Advanced:     extutil.Ptr(true),
				Order:        extutil.Ptr(101),
			},
			{
				Name:         "port",
				Label:        "Ports",
				Description:  extutil.Ptr("Restrict to/from which ports the traffic is affected."),
				Type:         action_kit_api.StringArray,
				DefaultValue: extutil.Ptr(""),
				Advanced:     extutil.Ptr(true),
				Order:        extutil.Ptr(102),
			},
		},
	}
}

func blackhole(runc runc.Runc) networkOptsProvider {
	return func(ctx context.Context, request action_kit_api.PrepareActionRequestBody) (network.BlackholeOpts, error) {
		containerId := request.Target.Attributes["container.id"][0]

		toResolve := append(
			toStrings(request.Config["ip"]),
			toStrings(request.Config["hostname"])...,
		)
		includeCidrs, err := network.ResolveHostnames(ctx, runc, RemovePrefix(containerId), toResolve...)
		if err != nil {
			return network.BlackholeOpts{}, err
		}
		if len(includeCidrs) == 0 {
			//if no hostname/ip specified we block all ips
			includeCidrs = []string{"::/0", "0.0.0.0/0"}
		}

		portRanges, err := parsePortRanges(toStrings(request.Config["port"]))
		if err != nil {
			return network.BlackholeOpts{}, err
		}
		if len(portRanges) == 0 {
			//if no hostname/ip specified we block all ports
			portRanges = []network.PortRange{network.PortRangeAny}
		}

		includes := network.NewCidrWithPortRanges(includeCidrs, portRanges...)
		var excludes []network.CidrWithPortRange

		//FXIME:
		//if request.ExecutionContext.RestrictedUrls != nil {
		//	for _, restrictedUrl := range *request.ExecutionContext.RestrictedUrls {
		//		ips, port, err := resolveUrl(ctx, runc, containerId, restrictedUrl)
		//		if err != nil {
		//			return network.BlackholeOpts{}, err
		//		}
		//
		//		excludes = append(excludes, network.NewCidrWithPortRanges(ips, network.PortRange{From: port, To: port})...)
		//	}
		//}

		return network.BlackholeOpts{
			Include: uniq(includes),
			Exclude: uniq(excludes),
		}, nil
	}
}
