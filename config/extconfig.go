// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package config

import (
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type Specification struct {
	Socket              string `json:"socket" required:"false"`
	Runtime             string `json:"runtime" required:"false"`
	ContainerdNamespace string `json:"containerdNamespace" required:"true" default:"k8s.io"`
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
