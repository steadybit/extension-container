// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	dockerparser "github.com/novln/docker-parser"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_commons"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	labelPrefixAppKubernetes = "app.kubernetes.io/"
)

type containerDiscovery struct {
	client types.Client
}

var (
	_ discovery_kit_sdk.TargetDescriber    = (*containerDiscovery)(nil)
	_ discovery_kit_sdk.AttributeDescriber = (*containerDiscovery)(nil)
)

func NewContainerDiscovery(client types.Client) discovery_kit_sdk.TargetDiscovery {
	discovery := &containerDiscovery{client: client}
	return discovery_kit_sdk.NewCachedTargetDiscovery(discovery,
		discovery_kit_sdk.WithTargetsRefreshTimeout(5*time.Minute),
		discovery_kit_sdk.WithRefreshTargetsNow(),
		discovery_kit_sdk.WithRefreshTargetsInterval(context.Background(), 30*time.Second),
	)
}

func (d *containerDiscovery) Describe() discovery_kit_api.DiscoveryDescription {
	return discovery_kit_api.DiscoveryDescription{
		Id: targetID,
		Discover: discovery_kit_api.DescribingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr(config.Config.DiscoveryCallInterval),
		},
	}
}

func (d *containerDiscovery) DescribeTarget() discovery_kit_api.TargetDescription {
	return discovery_kit_api.TargetDescription{
		Id:      targetID,
		Version: extbuild.GetSemverVersionStringOrUnknown(),
		Icon:    extutil.Ptr(targetIcon),

		// Labels used in the UI
		Label: discovery_kit_api.PluralLabel{One: "Container", Other: "Containers"},

		// Category for the targets to appear in
		Category: extutil.Ptr("basic"),

		// Specify attributes shown in table columns and to be used for sorting
		Table: discovery_kit_api.Table{
			Columns: []discovery_kit_api.Column{
				{Attribute: "k8s.container.name", FallbackAttributes: &[]string{"container.name"}},
				{Attribute: "k8s.pod.name"},
				{Attribute: "k8s.namespace"},
				{Attribute: "host.hostname"},
				{Attribute: "aws.zone", FallbackAttributes: &[]string{"google.zone", "azure.region", "azure.zone"}},
			},
			OrderBy: []discovery_kit_api.OrderBy{
				{Attribute: "k8s.container.name", Direction: discovery_kit_api.ASC},
				{Attribute: "k8s.container.name", Direction: discovery_kit_api.ASC},
			},
		},
	}
}

func (d *containerDiscovery) DescribeAttributes() []discovery_kit_api.AttributeDescription {
	return []discovery_kit_api.AttributeDescription{
		{
			Attribute: "container.name",
			Label:     discovery_kit_api.PluralLabel{One: "Container Name", Other: "Container Names"},
		},
		{
			Attribute: "container.host",
			Label:     discovery_kit_api.PluralLabel{One: "Container Host", Other: "Container Hosts"},
		},
		{
			Attribute: "container.image",
			Label:     discovery_kit_api.PluralLabel{One: "Container Image", Other: "Container Images"},
		},
		{
			Attribute: "container.image.registry",
			Label:     discovery_kit_api.PluralLabel{One: "Container Image Registry", Other: "Container Image Registries"},
		},
		{
			Attribute: "container.image.repository",
			Label:     discovery_kit_api.PluralLabel{One: "Container Image Repository", Other: "Container Image Repositories"},
		},
		{
			Attribute: "container.image.tag",
			Label:     discovery_kit_api.PluralLabel{One: "Container Image Tag", Other: "Container Image Tags"},
		},
		{
			Attribute: "container.id",
			Label:     discovery_kit_api.PluralLabel{One: "Container ID", Other: "Container IDs"},
		},
		{
			Attribute: "container.id.stripped",
			Label:     discovery_kit_api.PluralLabel{One: "Container ID (stripped)", Other: "Container IDs (stripped)"},
		},
		{
			Attribute: "container.engine",
			Label:     discovery_kit_api.PluralLabel{One: "Container Engine", Other: "Container Engines"},
		},
		{
			Attribute: "container.engine.version",
			Label:     discovery_kit_api.PluralLabel{One: "Container Engine Version", Other: "Container Engine Versions"},
		},
	}
}

func (d *containerDiscovery) DiscoverTargets(ctx context.Context) ([]discovery_kit_api.Target, error) {
	log.Info().Msg("Memory usage before DiscoverTargets")
	PrintMemUsage()
	containers, err := d.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	hostname, _ := os.Hostname()
	version, _ := d.client.Version(ctx)

	targets := make([]discovery_kit_api.Target, 0, len(containers))
	for _, container := range containers {
		if ignoreContainer(container) {
			continue
		}

		targets = append(targets, d.mapTarget(container, hostname, version))
	}
	result := discovery_kit_commons.ApplyAttributeExcludes(targets, config.Config.DiscoveryAttributesExcludes)
	log.Info().Msg("Memory usage after DiscoverTargets")
	PrintMemUsage()
	return result, nil

}

func PrintMemUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Info().Msgf("\tAlloc = %v MiB", m.Alloc / 1024 / 1024)
	log.Info().Msgf("\tTotalAlloc = %v MiB", m.TotalAlloc / 1024 / 1024)
	log.Info().Msgf("\tSys = %v MiB", m.Sys / 1024 / 1024)
	log.Info().Msgf("\tNumGC = %v\n", m.NumGC)
}

func ignoreContainer(container types.Container) bool {
	if label := container.Labels()["io.cri-containerd.kind"]; label == "sandbox" {
		return true
	}

	if label := container.Labels()["io.kubernetes.docker.type"]; label == "podsandbox" {
		return true
	}

	if label := container.Labels()["com.amazonaws.ecs.container-name"]; label == "~internal~ecs~pause" {
		return true
	}

	if config.Config.DisableDiscoveryExcludes {
		return false
	}

	if label := container.Labels()["steadybit.com.discovery-disabled"]; label == "true" {
		return true
	}

	if label := container.Labels()["steadybit.com/discovery-disabled"]; label == "true" {
		return true
	}

	if label := container.Labels()["com.steadybit.agent"]; label == "true" {
		return true
	}

	return false
}

func (d *containerDiscovery) mapTarget(container types.Container, hostname string, version string) discovery_kit_api.Target {
	attributes := make(map[string][]string)

	name := strings.TrimPrefix(container.Name(), "/")
	attributes["container.name"] = []string{name}
	if hostname != "" {
		attributes["container.host"] = []string{hostname}
		attributes["host.hostname"] = []string{hostname}
		attributes["container.host/name"] = []string{fmt.Sprintf("%s/%s", hostname, name)}
	}
	attributes["container.image"] = []string{container.ImageName()}

	if ref, _ := dockerparser.Parse(container.ImageName()); ref != nil {
		if ref.Repository() != "" {
			attributes["container.image.repository"] = []string{ref.Repository()}
		}
		if ref.Registry() != "" {
			attributes["container.image.registry"] = []string{ref.Registry()}
		}
		if ref.Tag() != "" {
			attributes["container.image.tag"] = []string{ref.Tag()}
		}
	}

	attributes["container.id"] = []string{AddPrefix(container.Id(), d.client.Runtime())}
	attributes["container.id.stripped"] = []string{container.Id()}
	attributes["container.engine"] = []string{string(d.client.Runtime())}
	if version != "" {
		attributes["container.engine.version"] = []string{version}
	}

	for key, value := range container.Labels() {
		addLabelOrK8sAttribute(attributes, key, value)
	}

	label := container.Id()
	if len(name) > 0 {
		label = name
	} else if container.Labels()["io.kubernetes.container.name"] != "" {
		label = fmt.Sprintf("%s_%s_%s",
			container.Labels()["io.kubernetes.pod.namespace"],
			container.Labels()["io.kubernetes.pod.name"],
			container.Labels()["io.kubernetes.container.name"],
		)
	}

	return discovery_kit_api.Target{
		Id:         container.Id(),
		Label:      label,
		TargetType: targetID,
		Attributes: attributes,
	}
}

func addLabelOrK8sAttribute(attributes map[string][]string, key, value string) {
	if strings.HasPrefix(key, labelPrefixAppKubernetes) {
		key = "k8s.app." + strings.TrimPrefix(key, labelPrefixAppKubernetes)
		attributes[key] = append(attributes[key], value)
		return
	}

	switch key {
	case "io.kubernetes.pod.name":
		key = "k8s.pod.name"
	case "io.kubernetes.pod.namespace":
		key = "k8s.namespace"
	case "io.kubernetes.container.name":
		key = "k8s.container.name"
	default:
		key = "container.label." + key
	}

	attributes[key] = append(attributes[key], value)
}
