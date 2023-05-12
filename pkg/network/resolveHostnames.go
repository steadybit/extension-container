// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"bytes"
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"net"
	"strings"
)

func ResolveHostnames(ctx context.Context, r runc.Runc, config TargetContainerConfig, ipOrHostnames ...string) ([]string, error) {
	hostnames, ips := classifyResolved(ipOrHostnames)

	if len(hostnames) == 0 {
		return ips, nil
	}

	var sb strings.Builder
	for _, hostname := range hostnames {
		sb.WriteString(hostname)
		sb.WriteString(" A\n")
		sb.WriteString(hostname)
		sb.WriteString(" AAAA\n")
	}
	stdin := strings.NewReader(sb.String())

	outb, err := runResolvingSidecar(ctx, r, config, stdin)
	if err != nil {
		return nil, fmt.Errorf("could not resolve hostnames: %w", err)
	}

	for _, ip := range strings.Split(string(outb), "\n") {
		ips = append(ips, strings.TrimSpace(ip))
	}

	log.Trace().Strs("ips", ips).Strs("ipOrHostnames", ipOrHostnames).Msg("resolved ips")
	return ips, nil
}

func runResolvingSidecar(ctx context.Context, r runc.Runc, config TargetContainerConfig, stdin *strings.Reader) ([]byte, error) {
	id := getNextContainerId()

	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return nil, err
	}

	if err := utils.CopyFileFromProcess(bundle, config.Pid, "/etc/resolv.conf"); err != nil {
		log.Warn().Err(err).Msg("could not copy /etc/resolv.conf")
	}

	if err := utils.CopyFileFromProcess(bundle, config.Pid, "/etc/hosts"); err != nil {
		log.Warn().Err(err).Msg("could not copy /etc/hosts")
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("dig", "-f-", "+timeout=4", "+short", "+nottlid", "+noclass"),
	); err != nil {
		return nil, err
	}

	var outb, errb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{Stdin: stdin, Stdout: &outb, Stderr: &errb})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, errb.String())
	}
	return outb.Bytes(), nil
}

func classifyResolved(ipOrHostnames []string) (unresolved, resolved []string) {
	for _, ipOrHostname := range ipOrHostnames {
		if ip := net.ParseIP(strings.TrimPrefix(strings.TrimSuffix(ipOrHostname, "]"), "[")); ip == nil {
			unresolved = append(unresolved, ipOrHostname)
		} else {
			resolved = append(resolved, ip.String())
		}
	}
	return unresolved, resolved
}
