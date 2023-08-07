// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"fmt"
	dockerparser "github.com/novln/docker-parser"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-container/pkg/container/types"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/exthttp"
	"github.com/steadybit/extension-kit/extutil"
	"net/http"
	"os"
	"strings"
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
	exthttp.RegisterHttpHandler(discoveryBasePath+"/discovered-targets", discovery.getDiscoveredTargets)
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
	}
}

func getDiscoveryDescription() discovery_kit_api.DiscoveryDescription {
	return discovery_kit_api.DiscoveryDescription{
		Id:         targetID,
		RestrictTo: extutil.Ptr(discovery_kit_api.LEADER),
		Discover: discovery_kit_api.DescribingEndpointReferenceWithCallInterval{
			Method:       "GET",
			Path:         discoveryBasePath + "/discovered-targets",
			CallInterval: extutil.Ptr("1m"),
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
				{Attribute: "container.host"},
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
func (d *containerDiscovery) getDiscoveredTargets(w http.ResponseWriter, r *http.Request, _ []byte) {
	containers, err := d.client.List(r.Context())
	if err != nil {
		exthttp.WriteError(w, extension_kit.ToError("Could not list containers.", err))
		return
	}

	hostname, _ := os.Hostname()
	version, _ := d.client.Version(r.Context())

	var targets []discovery_kit_api.Target
	targets = []discovery_kit_api.Target{}
	for _, container := range containers {
		if ignoreContainer(container) {
			continue
		}

		targets = append(targets, d.mapTarget(container, hostname, version))
	}

	exthttp.WriteBody(w, discovery_kit_api.DiscoveredTargets{Targets: targets})
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
		key = "k8s.pod.namespace"
	case "io.kubernetes.container.name":
		key = "k8s.container.name"
	default:
		key = "container.label." + key
	}

	attributes[key] = append(attributes[key], value)
}
