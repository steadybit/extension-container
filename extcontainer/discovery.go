// Copyright 2025 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"context"
	"fmt"
	dockerparser "github.com/novln/docker-parser"
	"github.com/steadybit/action-kit/go/action_kit_commons/utils"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_commons"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"net"
	"os"
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
				{Attribute: "aws.zone", FallbackAttributes: &[]string{"google.zone", "azure.zone"}},
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
	hostname, fqdn := d.getHostname()
	version, _ := d.client.Version(ctx)

	containers, err := d.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	targets := make([]discovery_kit_api.Target, 0, len(containers))
	for _, container := range containers {
		if ignoreContainer(container) {
			continue
		}

		targets = append(targets, d.mapTarget(container, hostname, fqdn, version))
	}
	return discovery_kit_commons.ApplyAttributeExcludes(targets, config.Config.DiscoveryAttributesExcludes), nil
}

func ignoreContainer(container types.Container) bool {
	labels := container.Labels()

	if hasDisallowedK8sNamespaceLabel(labels) {
		return true
	}

	if labels["io.cri-containerd.kind"] == "sandbox" {
		return true
	}

	if labels["io.kubernetes.docker.type"] == "podsandbox" {
		return true
	}

	if labels["com.amazonaws.ecs.container-name"] == "~internal~ecs~pause" {
		return true
	}

	if config.Config.DisableDiscoveryExcludes {
		return false
	}

	if labels["steadybit.com.discovery-disabled"] == "true" {
		return true
	}

	if labels["steadybit.com/discovery-disabled"] == "true" {
		return true
	}

	if labels["com.steadybit.agent"] == "true" {
		return true
	}

	return false
}

func (d *containerDiscovery) mapTarget(container types.Container, hostname, fqdn string, version string) discovery_kit_api.Target {
	attributes := make(map[string][]string)

	name := strings.TrimPrefix(container.Name(), "/")
	attributes["container.name"] = []string{name}
	if hostname != "" {
		attributes["container.host"] = []string{hostname}
		attributes["host.hostname"] = []string{hostname}
		attributes["host.domainname"] = []string{fqdn}
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

	labels := container.Labels()
	for key, value := range labels {
		addLabelOrK8sAttribute(attributes, key, value)
	}

	label := container.Id()
	if len(name) > 0 {
		label = name
	} else if labels["io.kubernetes.container.name"] != "" {
		label = fmt.Sprintf("%s_%s_%s",
			labels["io.kubernetes.pod.namespace"],
			labels["io.kubernetes.pod.name"],
			labels["io.kubernetes.container.name"],
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
		key = fmt.Sprintf("k8s.app.%s", strings.TrimPrefix(key, labelPrefixAppKubernetes))
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
		key = fmt.Sprintf("container.label.%s", key)
	}

	attributes[key] = append(attributes[key], value)
}

func (d *containerDiscovery) getHostname() (hostname, fqdn string) {
	hostname = config.Config.Hostname

	if hostname == "" {
		hostname, _ = getHostnameFromInitProcess()
	}

	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	if hostname != "" {
		fqdn, _ = resolveFQDN(context.Background(), hostname)
		if fqdn == "" {
			fqdn = hostname
		}
	} else {
		hostname = "unknown"
		fqdn = "unknown"
	}
	return
}

// inspired by elastic/go-sysinfo
func resolveFQDN(ctx context.Context, hostname string) (string, error) {
	var errs error
	cname, err := net.DefaultResolver.LookupCNAME(ctx, hostname)
	if err != nil {
		errs = fmt.Errorf("could not get FQDN, all methods failed: failed looking up CNAME: %w", err)
	}

	if cname != "" {
		cname = strings.TrimSuffix(cname, ".")

		if strings.EqualFold(cname, hostname) {
			return hostname, nil
		}

		return cname, nil
	}

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		errs = fmt.Errorf("%s: failed looking up IP: %w", errs, err)
	}

	for _, ip := range ips {
		names, err := net.DefaultResolver.LookupAddr(ctx, ip.String())
		if err != nil || len(names) == 0 {
			continue
		}
		return strings.TrimSuffix(names[0], "."), nil
	}

	return "", errs
}

func getHostnameFromInitProcess() (string, error) {
	if out, err := utils.RootCommandContext(context.Background(), "nsenter", "-t", "1", "-u", "--", "hostname").CombinedOutput(); err == nil {
		return strings.TrimSpace(string(out)), err
	} else {
		return "", err
	}
}
