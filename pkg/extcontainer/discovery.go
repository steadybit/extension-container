// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"fmt"
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
		Category: extutil.Ptr("Container"),

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
			//TODO
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

	targets := make([]discovery_kit_api.Target, len(containers))
	for i, container := range containers {
		targets[i] = d.mapTarget(container, hostname, version)
	}

	exthttp.WriteBody(w, discovery_kit_api.DiscoveredTargets{Targets: targets})
}

func (d *containerDiscovery) mapTarget(container types.Container, hostname string, version string) discovery_kit_api.Target {
	attributes := make(map[string][]string)

	attributes["container.name"] = container.Names()
	if hostname != "" {
		attributes["container.host"] = []string{hostname}
		for _, name := range container.Names() {
			attributes["container.host/name"] = append(attributes["container.name"], fmt.Sprintf("%s/%s", hostname, name))
		}
	}
	attributes["container.image"] = []string{container.ImageName()}
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
		key = "label." + key
	}

	attributes[key] = append(attributes[key], value)
}
