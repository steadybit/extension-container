// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
)

type Interface struct {
	Index    uint     `json:"ifindex"`
	Name     string   `json:"ifname"`
	LinkType string   `json:"link_type"`
	Flags    []string `json:"flags"`
}

func (i *Interface) HasFlag(f string) bool {
	for _, flag := range i.Flags {
		if flag == f {
			return true
		}
	}
	return false
}

type ExtraMount struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

func ListInterfaces(ctx context.Context, r runc.Runc, config TargetContainerConfig) ([]Interface, error) {
	id := getNextContainerId()

	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return nil, err
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithHostname(fmt.Sprintf("ip-link-show-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("ip", "-json", "link", "show"),
	); err != nil {
		return nil, err
	}

	var outb, errb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{Stdout: &outb, Stderr: &errb})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return nil, fmt.Errorf("could not list interfaces: %w: %s", err, errb.String())
	}

	var interfaces []Interface
	err = json.Unmarshal(outb.Bytes(), &interfaces)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal interfaces: %w", err)
	}

	log.Trace().Interface("interfaces", interfaces).Msg("listed network interfaces")
	return interfaces, nil
}
