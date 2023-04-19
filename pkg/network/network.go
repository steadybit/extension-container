// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	runc2 "github.com/steadybit/extension-container/pkg/container/runc"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

var counter = atomic.Int32{}

type Mode string
type Family string

const (
	ModeAdd    Mode   = "add"
	ModeDelete Mode   = "del"
	FamilyV4   Family = "4"
	FamilyV6   Family = "6"
)

type NetworkOpts interface {
	Statements(family Family, mode Mode) io.Reader
}

type CidrWithPortRange struct {
	Cidr    string
	PortMin int
	PortMax int
}

type BlackholeOpts struct {
	Include []CidrWithPortRange
	Exclude []CidrWithPortRange
}

func (o *BlackholeOpts) Statements(family Family, mode Mode) (io.Reader, error) {
	var statements []string

	for _, tuple := range o.Include {
		ip, portRange, err := parseIpPortRange(tuple)
		if err != nil {
			return nil, err
		}

		if getFamily(ip) != family {
			continue
		}

		statements = append(statements, fmt.Sprintf("%s blackhole to %s dport %d", mode, ip, portRange))
		statements = append(statements, fmt.Sprintf("%s blackhole from %s sport %d", mode, ip, portRange))
	}

	for _, tuple := range o.Exclude {
		cidr, portRange, err := parseIpPortRange(tuple)
		if err != nil {
			return nil, err
		}

		if getFamily(cidr) != family {
			continue
		}

		statements = append(statements, fmt.Sprintf("%s to %s dport %d table main", mode, cidr, portRange))
		statements = append(statements, fmt.Sprintf("%s from %s sport %d table main", mode, cidr, portRange))
	}

	return toReader(statements, mode)
}

func toReader(statements []string, mode Mode) (io.Reader, error) {
	sb := strings.Builder{}

	if mode == ModeAdd {
		for _, statement := range statements {
			fmt.Fprintf(&sb, "%s\n", statement)
		}
	} else {
		for i := len(statements) - 1; i >= 0; i-- {
			statement := statements[i]
			fmt.Fprintf(&sb, "%s\n", statement)
		}
	}

	return strings.NewReader(sb.String()), nil
}

func getFamily(ip net.IP) Family {
	switch {
	case ip.To4() != nil:
		return FamilyV4
	case ip.To16() != nil:
		return FamilyV6
	default:
		return ""
	}
}

func parseIpPortRange(tuple string) (net.IP, *int, error) {
	host, port, err := net.SplitHostPort(tuple)

	if err != nil {
		switch err.(type) {
		case *net.AddrError:
			if err.(*net.AddrError).Err == "missing port in address" {
				host = tuple
				port = ""
			}
		default:
			return net.IPv4zero, nil, err
		}
	}

	numPort, err := strconv.Atoi(port)
	if err != nil {
		return net.IPv4zero, nil, err
	}

	return net.ParseIP(host), &numPort, nil
}

func Apply(r runc2.Runc, targetId string, opts NetworkOpts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("applying network config")
	return runBatch(r, targetId, opts.IpBatch())
}

func Revert(r runc2.Runc, targetId string, opts NetworkOpts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("reverting network config")
	return runBatch(r, targetId, opts.RevertBatch())
}

func runBatch(r runc2.Runc, targetId string, batch io.Reader) error {
	ctx := context.Background()

	state, err := r.State(ctx, targetId)
	if err != nil {
		return fmt.Errorf("could not load state of target container: %w", err)
	}

	id := fmt.Sprintf("sb-network-%d", counter.Add(1))
	bundleDir := filepath.Join("/tmp/steadybit/containers", id)
	rootfs := filepath.Join(bundleDir, "rootfs")

	_ = os.RemoveAll(bundleDir)

	if err := os.MkdirAll(rootfs, 0775); err != nil {
		return fmt.Errorf("failed to create bundle dir: %w", err)
	}

	if out, err := exec.Command("tar", "-xf", "stress-ng.tar", "-C", rootfs).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to prepare rootfs dir: %s %w", out, err)
	}

	if err := r.Spec(ctx, bundleDir); err != nil {
		return err
	}

	if err := runc2.EditSpec(bundleDir, func(spec *specs.Spec) {
		spec.Hostname = id
		spec.Annotations = map[string]string{
			"com.steadybit.sidecar": "true",
		}
		spec.Root.Path = "rootfs"
		spec.Root.Readonly = true
		spec.Process.Args = []string{"ip", "-batch", "-"}
		spec.Process.Terminal = false
		spec.Process.Cwd = "/tmp"

		runc2.UseCgroupOf(spec, state.Pid, "network")
		runc2.UseNamespacesOf(spec, state.Pid)
		runc2.UseFileOf(spec, state.Pid, "/etc/hosts")
		runc2.UseFileOf(spec, state.Pid, "/etc/resolv.conf")
	}); err != nil {
		return err
	}

	err = r.Run(ctx, id, bundleDir, runc2.NewStdIoOpts().WithStdin(batch))
	defer func() {
		_ = r.Delete(context.Background(), id, true)
		_ = os.RemoveAll(bundleDir)
	}()
	return err
}
