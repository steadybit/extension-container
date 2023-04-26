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
	"net"
	"strings"
)

func ResolveHostnames(ctx context.Context, r runc.Runc, targetId string, ipOrHostnames ...string) ([]string, error) {
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

	id := getNextContainerId()
	bundle, cleanup, err := createBundleAndSpec(ctx, r, id, targetId, func(spec *specs.Spec) {
		spec.Process.Args = []string{"dig", "-f-", "+timeout=4", "+short", "+nottlid", "+noclass"}
	})
	defer func() { _ = cleanup() }()
	if err != nil {
		return nil, err
	}

	var outb, errb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{Stdin: stdin, Stdout: &outb, Stderr: &errb})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return nil, fmt.Errorf("could not resolve hostnames: %w: %s", err, errb.String())
	}

	for _, ip := range strings.Split(outb.String(), "\n") {
		ips = append(ips, strings.TrimSpace(ip))
	}

	log.Trace().Strs("ips", ips).Strs("ipOrHostnames", ipOrHostnames).Msg("resolved ips")
	return ips, nil
}

func classifyResolved(ipOrHostnames []string) (unresolved, resolved []string) {
	for _, ipOrHostnames := range ipOrHostnames {
		if ip := net.ParseIP(strings.TrimPrefix(strings.TrimSuffix(ipOrHostnames, "]"), "[")); ip == nil {
			unresolved = append(unresolved, ipOrHostnames)
		} else {
			resolved = append(resolved, ip.String())
		}
	}
	return unresolved, resolved
}
