// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package main

import (
	"context"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer"
	"github.com/steadybit/extension-container/extcontainer/container"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/exthealth"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extlogging"
	"github.com/steadybit/extension-kit/extruntime"
	"github.com/steadybit/extension-kit/extsignals"
	_ "go.uber.org/automaxprocs" // Importing automaxprocs automatically adjusts GOMAXPROCS.
	// You can find more details of its behavior from the doc comment of memlimit.SetGoMemLimitWithEnv.
	_ "github.com/KimMachineGun/automemlimit" // By default, it sets `GOMEMLIMIT` to 90% of cgroup's memory limit.
)

func main() {
	extlogging.InitZeroLog()

	extbuild.PrintBuildInformation()
	extruntime.LogRuntimeInformation(zerolog.InfoLevel)

	config.ParseConfiguration()
	config.ValidateConfiguration()

	log.Debug().Any("config", config.Config).Msg("Configuration loaded.")

	exthealth.SetReady(false)
	exthealth.StartProbes(int(config.Config.HealthPort))

	exthttp.RegisterHttpHandler("/", exthttp.GetterAsHandler(getExtensionList))

	client, err := container.NewClient()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create container engine client.")
	}

	stopCheck := container.RegisterLivenessCheck(client)
	defer close(stopCheck)

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

	ociRuntimeCfg := ociruntime.ConfigFromEnvironment()
	if ociRuntimeCfg.Root == "" {
		ociRuntimeCfg.Root = client.Runtime().DefaultRuncRoot()
	}
	r := ociruntime.NewOciRuntimeWithCrunForSidecars(ociRuntimeCfg)

	discovery_kit_sdk.Register(extcontainer.NewContainerDiscovery(client))
	action_kit_sdk.RegisterAction(extcontainer.NewPauseContainerAction(client))
	action_kit_sdk.RegisterAction(extcontainer.NewStopContainerAction(client))
	action_kit_sdk.RegisterAction(extcontainer.NewStressCpuContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewStressMemoryContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewStressIoContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkBlackholeContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkBlockDnsContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkDelayContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkLimitBandwidthContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkCorruptPackagesContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewNetworkPackageLossContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewFillDiskContainerAction(r, client))
	action_kit_sdk.RegisterAction(extcontainer.NewFillMemoryContainerAction(r, client))

	extsignals.ActivateSignalHandlers()

	action_kit_sdk.RegisterCoverageEndpoints()

	exthealth.SetReady(true)

	exthttp.Listen(exthttp.ListenOpts{
		Port: int(config.Config.Port),
	})
}

type ExtensionListResponse struct {
	action_kit_api.ActionList       `json:",inline"`
	discovery_kit_api.DiscoveryList `json:",inline"`
}

func getExtensionList() ExtensionListResponse {
	return ExtensionListResponse{
		ActionList:    action_kit_sdk.GetActionList(),
		DiscoveryList: discovery_kit_sdk.GetDiscoveryList(),
	}
}
