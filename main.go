// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package main

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/event-kit/go/event_kit_api"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/pkg/container"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/extcontainer"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extlogging"
)

func main() {
	extlogging.InitZeroLog()

	extbuild.PrintBuildInformation()

	config.ParseConfiguration()
	config.ValidateConfiguration()

	exthttp.RegisterHttpHandler("/", exthttp.GetterAsHandler(getExtensionList))

	client, err := container.NewClient()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create container engine client.")
	}
	defer func(client types.Client) {
		err := client.Close()
		if err != nil {
			log.Error().Err(err).Msg("Failed to close container engine client.")
		}
	}(client)
	version, _ := client.Version(context.Background())
	log.Info().
		Str("engine", string(client.Runtime())).
		Str("version", version).
		Str("socket", client.Socket()).
		Msg("Container runtime client initialized.")

	r := runc.Runc{Root: client.Runtime().DefaultRuncRoot()}

	extcontainer.RegisterDiscoveryHandlers(client)
	action_kit_sdk.RegisterAction(extcontainer.NewPauseContainerAction(client))
	action_kit_sdk.RegisterAction(extcontainer.NewStopContainerAction(client))
	action_kit_sdk.RegisterAction(extcontainer.NewStressCpuContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewStressMemoryContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewStressIoContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkBlackholeContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkBlockDnsContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkDelayContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkLimitBandwidthContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkCorruptPackagesContainerAction(r))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkPackageLossContainerAction(r))

	exthttp.Listen(exthttp.ListenOpts{
		Port: 8080,
	})
}

type ExtensionListResponse struct {
	action_kit_api.ActionList       `json:",inline"`
	discovery_kit_api.DiscoveryList `json:",inline"`
	event_kit_api.EventListenerList `json:",inline"`
}

func getExtensionList() ExtensionListResponse {
	return ExtensionListResponse{
		ActionList:    action_kit_sdk.GetActionList(),
		DiscoveryList: extcontainer.GetDiscoveryList(),
	}
}
