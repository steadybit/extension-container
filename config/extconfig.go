// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package config

import (
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type Specification struct {
	Socket              string `json:"containerSocket" split_words:"true" required:"false"`
	Runtime             string `json:"containerRuntime" split_words:"true" required:"false"`
	ContainerdNamespace string `json:"containerdNamespace" split_words:"true" required:"true" default:"k8s.io"`
	RuncRoot            string `json:"runcRoot" split_words:"true" required:"false"`
	RuncDebug           bool   `json:"runcDebug" split_words:"true" required:"false"`
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
