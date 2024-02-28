// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package config

import (
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type Specification struct {
	ContainerSocket             string   `json:"containerSocket" split_words:"true" required:"false"`
	ContainerRuntime            string   `json:"containerRuntime" split_words:"true" required:"false"`
	ContainerdNamespace         string   `json:"containerdNamespace" split_words:"true" required:"true" default:"k8s.io"`
	DisableDiscoveryExcludes    bool     `required:"false" split_words:"true" default:"false"`
	DiscoveryCallInterval       string   `json:"discoveryCallInterval" split_words:"true" required:"false" default:"15s"`
	DiscoveryAttributesExcludes []string `json:"discoveryAttributesExcludes" split_words:"true" required:"false" default:"container.label.io.buildpacks.lifecycle.metadata,container.label.io.buildpacks.build.metadata"`
	Port                        uint16   `json:"port" split_words:"true" required:"false" default:"8086"`
	HealthPort                  uint16   `json:"healthPort" split_words:"true" required:"false" default:"8082"`
}

var (
	Config Specification
)

func ParseConfiguration() {
	err := envconfig.Process("steadybit_extension", &Config)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse configuration from environment.")
	}
}

func ValidateConfiguration() {
	if Config.DisableDiscoveryExcludes {
		log.Info().Msg("Discovery excludes are disabled. Will also discover containers labeled with steadybit.com/discovery-exclude=true.")
	}
}
