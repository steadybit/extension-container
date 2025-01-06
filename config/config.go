// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package config

import (
	"flag"
	"github.com/gobwas/glob"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
	"os"
	"slices"
	"strings"
)

type Specification struct {
	ContainerSocket             string           `json:"containerSocket" split_words:"true" required:"false"`
	ContainerRuntime            string           `json:"containerRuntime" split_words:"true" required:"false"`
	ContainerdNamespace         string           `json:"containerdNamespace" split_words:"true" required:"true" default:"k8s.io"`
	DisableDiscoveryExcludes    bool             `required:"false" split_words:"true" default:"false"`
	DiscoveryCallInterval       string           `json:"discoveryCallInterval" split_words:"true" required:"false" default:"15s"`
	DiscoveryAttributesExcludes []string         `json:"discoveryAttributesExcludes" split_words:"true" required:"false" default:"container.label.io.buildpacks.lifecycle.metadata,container.label.io.buildpacks.build.metadata"`
	Port                        uint16           `json:"port" split_words:"true" required:"false" default:"8086"`
	HealthPort                  uint16           `json:"healthPort" split_words:"true" required:"false" default:"8082"`
	LivenessCheckInterval       string           `json:"livenessProbeInterval" split_words:"true" required:"false" default:"30s"` // 0 or empty string disables liveness check
	MemfillPath                 string           `json:"memfillPath" split_words:"true" required:"true"`
	Hostname                    string           `json:"hostname" split_words:"true" required:"false"`
	DisallowHostNetwork         bool             `json:"disallowHostNetwork" split_words:"true" required:"false" default:"false"`
	DisallowK8sNamespaces       []DisallowedName `json:"disallowK8sNamespaces" split_words:"true" required:"false"`
}

var (
	Config Specification
)

func ParseConfiguration() {
	if err := envconfig.Process("steadybit_extension", &Config); err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse configuration from environment.")
	}

	if err := parseArgs(&Config); err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse command line arguments.")
	}
}

func parseArgs(cfg *Specification) error {
	f := flag.NewFlagSet("config", flag.ContinueOnError)
	var disallowHostNetwork = f.Bool("disallowHostNetwork", false, "Disallow network attacks on host network containers")
	var disallowK8sNamespaces = f.String("disallowK8sNamespaces", "", "Disallow attacks on these k8s namespaces")

	if err := f.Parse(os.Args[1:]); err != nil {
		return err
	}

	cfg.DisallowHostNetwork = cfg.DisallowHostNetwork || *disallowHostNetwork

	for _, s := range strings.Split(strings.TrimSpace(*disallowK8sNamespaces), ",") {
		if s == "" {
			continue
		}
		var d DisallowedName
		if err := d.Decode(s); err != nil {
			return err
		}
		if !slices.Contains(cfg.DisallowK8sNamespaces, d) {
			cfg.DisallowK8sNamespaces = append(cfg.DisallowK8sNamespaces, d)
		}
	}

	return nil
}

func ValidateConfiguration() {
	if Config.DisableDiscoveryExcludes {
		log.Info().Msg("Discovery excludes are disabled. Will also discover containers labeled with steadybit.com/discovery-disabled.")
	}
}

type DisallowedName struct {
	g glob.Glob
}

func (d DisallowedName) Match(value string) bool {
	return d.g.Match(value)
}

func (d *DisallowedName) Decode(value string) error {
	g, err := glob.Compile(value)
	if err != nil {
		return err
	}
	d.g = g
	return nil
}
