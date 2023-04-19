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
	"os"
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

	state, err := r.State(ctx, targetId)
	if err != nil {
		return nil, fmt.Errorf("could not load state of target container: %w", err)
	}

	id := fmt.Sprintf("sb-network-%d", counter.Add(1))
	bundleDir, err := r.PrepareBundle(ctx, id)
	defer func() { _ = os.RemoveAll(bundleDir) }()
	if err != nil {
		return nil, err
	}

	umount, err := runc.MountFileOf(ctx, bundleDir, state.Pid, "/etc/hosts")
	defer func() { _ = umount() }()
	if err != nil {
		return nil, err
	}

	umount, err = runc.MountFileOf(ctx, bundleDir, state.Pid, "/etc/resolv.conf")
	defer func() { _ = umount() }()
	if err != nil {
		return nil, err
	}

	if err := runc.EditSpec(bundleDir, func(spec *specs.Spec) {
		spec.Hostname = id
		spec.Annotations = map[string]string{
			"com.steadybit.sidecar": "true",
		}
		spec.Root.Path = "rootfs"
		spec.Root.Readonly = true
		spec.Process.Args = []string{"dig", "-f-", "+timeout=4", "+short", "+nottlid", "+noclass"}
		spec.Process.Terminal = false
		spec.Process.Cwd = "/tmp"

		runc.UseCgroupOf(spec, state.Pid, "network")
		runc.UseNamespacesOf(spec, state.Pid)
	}); err != nil {
		return nil, err
	}

	var outb bytes.Buffer
	err = r.Run(ctx, id, bundleDir, runc.IoOpts{
		Stdin:  stdin,
		Stdout: &outb,
		Stderr: &outb,
	})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return nil, fmt.Errorf("could not resolve hostnames: %w: %s", err, outb.String())
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
