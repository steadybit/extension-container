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
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extutil"
	"os"
	"strings"
	"time"
)

const (
	discoveryBasePath        = basePath + "/discovery"
	labelPrefixAppKubernetes = "app.kubernetes.io/"
)

type containerDiscovery struct {
	client types.Client
}

func RegisterDiscoveryHandlers(client types.Client) {
	exthttp.RegisterHttpHandler(discoveryBasePath, exthttp.GetterAsHandler(getDiscoveryDescription))
	exthttp.RegisterHttpHandler(discoveryBasePath+"/target-description", exthttp.GetterAsHandler(getTargetDescription))
	exthttp.RegisterHttpHandler(discoveryBasePath+"/attribute-descriptions", exthttp.GetterAsHandler(getAttributeDescriptions))
	discovery := containerDiscovery{client}
	log.Info().Msgf("Starting container fetchData in background every %s", 30*time.Second)
	exthttp.RegisterHttpHandler(discoveryBasePath+"/discovered-targets", discoveryHandler(schedule(context.Background(), 30*time.Second, discovery.getDiscoveredTargets)))
}

func GetDiscoveryList() discovery_kit_api.DiscoveryList {
	return discovery_kit_api.DiscoveryList{
		Discoveries: []discovery_kit_api.DescribingEndpointReference{
			{
				Method: "GET",
				Path:   discoveryBasePath,
			},
		},
		TargetTypes: []discovery_kit_api.DescribingEndpointReference{
			{
				Method: "GET",
				Path:   discoveryBasePath + "/target-description",
			},
		},
		TargetAttributes: []discovery_kit_api.DescribingEndpointReference{
			{
				Method: "GET",
				Path:   discoveryBasePath + "/attribute-descriptions",
			},
		},
		TargetEnrichmentRules: []discovery_kit_api.DescribingEndpointReference{},
	}
}

func getDiscoveryDescription() discovery_kit_api.DiscoveryDescription {
	return discovery_kit_api.DiscoveryDescription{
		Id:         targetID,
		RestrictTo: extutil.Ptr(discovery_kit_api.LEADER),
		Discover: discovery_kit_api.DescribingEndpointReferenceWithCallInterval{
			Method:       "GET",
			Path:         discoveryBasePath + "/discovered-targets",
			CallInterval: extutil.Ptr(config.Config.DiscoveryCallInterval),
		},
	}
}

func getTargetDescription() discovery_kit_api.TargetDescription {
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

func getAttributeDescriptions() discovery_kit_api.AttributeDescriptions {
	return discovery_kit_api.AttributeDescriptions{
		Attributes: []discovery_kit_api.AttributeDescription{
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
		},
	}
}
func (d *containerDiscovery) getDiscoveredTargets(ctx context.Context) ([]discovery_kit_api.Target, error) {
	containers, err := d.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not list containers: %w", err)
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
	return discovery_kit_commons.ApplyAttributeExcludes(targets, config.Config.DiscoveryAttributesExcludes), nil
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

	if label := container.Labels()["com.steadybit.agent"]; label == "true" {
		return true
	}

	return false
}

func (d *containerDiscovery) mapTarget(container types.Container, hostname string, version string) discovery_kit_api.Target {
	attributes := make(map[string][]string)

	containerNames := container.Names()
	for i, name := range containerNames {
		containerNames[i] = strings.TrimPrefix(name, "/")
	}
	attributes["container.name"] = containerNames
	if hostname != "" {
		attributes["container.host"] = []string{hostname}
		attributes["host.hostname"] = []string{hostname}
		for _, name := range containerNames {
			attributes["container.host/name"] = append(attributes["container.name"], fmt.Sprintf("%s/%s", hostname, name))
		}
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
	if len(container.Names()) > 0 {
		label = container.Names()[0]
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
