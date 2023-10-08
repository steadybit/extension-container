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
	RuncRoot                    string   `json:"runcRoot" split_words:"true" required:"false"`
	RuncRootless                string   `json:"runcRootless" split_words:"true" required:"false"`
	RuncSystemdCgroup           bool     `json:"runcSystemdCgroup" split_words:"true" required:"false"`
	RuncDebug                   bool     `json:"runcDebug" split_words:"true" required:"false"`
	DisableDiscoveryExcludes    bool     `required:"false" split_words:"true" default:"false"`
	DiscoveryCallInterval       string   `json:"discoveryCallInterval" split_words:"true" required:"false" default:"30s"`
	Port                        uint16   `json:"port" split_words:"true" required:"false" default:"8086"`
	HealthPort                  uint16   `json:"healthPort" split_words:"true" required:"false" default:"8082"`
	DiscoveryAttributesExcludes []string `json:"discoveryAttributesExcludes" split_words:"true" required:"false"`
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
	// You may optionally validate the configuration here.
}
