// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
)

var counter = atomic.Int32{}

type BlackholeOpts struct {
	Include []CidrWithPortRange
	Exclude []CidrWithPortRange
}

func (o *BlackholeOpts) Statements(family Family, mode Mode) (io.Reader, error) {
	var statements []string

	for _, cidrAndPort := range o.Include {
		cidr := cidrAndPort.Cidr
		portRange := cidrAndPort.PortRange

		if cidrFamily, err := getFamily(cidr); err != nil {
			return nil, err
		} else if cidrFamily != family {
			continue
		}

		statements = append(statements, fmt.Sprintf("rule %s blackhole to %s dport %s", mode, cidr, portRange.String()))
		statements = append(statements, fmt.Sprintf("rule %s blackhole from %s sport %s", mode, cidr, portRange.String()))
	}

	for _, cidrAndPort := range o.Exclude {
		cidr := cidrAndPort.Cidr
		portRange := cidrAndPort.PortRange

		if cidrFamily, err := getFamily(cidr); err != nil {
			return nil, err
		} else if cidrFamily != family {
			continue
		}

		statements = append(statements, fmt.Sprintf("rule %s to %s dport %s table main", mode, cidr, portRange.String()))
		statements = append(statements, fmt.Sprintf("rule %s from %s sport %s table main", mode, cidr, portRange.String()))
	}

	log.Debug().Strs("statements", statements).Str("family", string(family)).Msg("generated ip statements")
	return toReader(statements, mode)
}

func toReader(statements []string, mode Mode) (io.Reader, error) {
	if len(statements) == 0 {
		return nil, nil
	}

	sb := strings.Builder{}

	if mode == ModeAdd {
		for _, statement := range statements {
			_, err := fmt.Fprintf(&sb, "%s\n", statement)
			if err != nil {
				return nil, err
			}
		}
	} else {
		for i := len(statements) - 1; i >= 0; i-- {
			statement := statements[i]
			_, err := fmt.Fprintf(&sb, "%s\n", statement)
			if err != nil {
				return nil, err
			}
		}
	}

	return strings.NewReader(sb.String()), nil
}

func getFamily(cidr string) (Family, error) {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}

	switch {
	case ip.To4() != nil:
		return FamilyV4, nil
	case ip.To16() != nil:
		return FamilyV6, nil
	default:
		return "", nil
	}
}

func Apply(ctx context.Context, r runc.Runc, targetId string, opts NetworkOpts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("applying network config")

	statementsV4, err := opts.Statements(FamilyV4, ModeAdd)
	if err != nil {
		return err
	}
	statementsV6, err := opts.Statements(FamilyV6, ModeAdd)
	if err != nil {
		return err
	}

	err = executeIpBatch(ctx, r, targetId, FamilyV4, statementsV4)
	if err != nil {
		return err
	}
	return executeIpBatch(ctx, r, targetId, FamilyV6, statementsV6)
}

func Revert(ctx context.Context, r runc.Runc, targetId string, opts NetworkOpts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("reverting network config")

	statementsV4, err := opts.Statements(FamilyV4, ModeDelete)
	if err != nil {
		return err
	}
	statementsV6, err := opts.Statements(FamilyV6, ModeDelete)
	if err != nil {
		return err
	}

	err = executeIpBatch(ctx, r, targetId, FamilyV4, statementsV4)
	if err != nil {
		return err
	}
	return executeIpBatch(ctx, r, targetId, FamilyV6, statementsV6)
}

func executeIpBatch(ctx context.Context, r runc.Runc, targetId string, family Family, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	state, err := r.State(ctx, targetId)
	if err != nil {
		return fmt.Errorf("could not load state of target container: %w", err)
	}

	id := fmt.Sprintf("sb-network-%d", counter.Add(1))
	bundleDir, err := r.PrepareBundle(ctx, id)
	defer func() { _ = os.RemoveAll(bundleDir) }()
	if err != nil {
		return err
	}

	umount, err := runc.MountFileOf(ctx, bundleDir, state.Pid, "/etc/hosts")
	defer func() { _ = umount() }()
	if err != nil {
		return err
	}

	umount, err = runc.MountFileOf(ctx, bundleDir, state.Pid, "/etc/resolv.conf")
	defer func() { _ = umount() }()
	if err != nil {
		return err
	}

	if err := runc.EditSpec(bundleDir, func(spec *specs.Spec) {
		spec.Hostname = id
		spec.Annotations = map[string]string{
			"com.steadybit.sidecar": "true",
		}
		spec.Root.Path = "rootfs"
		spec.Root.Readonly = true
		spec.Process.Args = []string{"ip", "-family", string(family), "-force", "-batch", "-"}
		spec.Process.Terminal = false
		spec.Process.Cwd = "/tmp"

		runc.AddCapabilities(spec, "CAP_NET_ADMIN")
		runc.UseCgroupOf(spec, state.Pid, "network")
		runc.UseNamespacesOf(spec, state.Pid)
	}); err != nil {
		return err
	}

	err = r.Run(ctx, id, bundleDir, runc.InheritStdIo().WithStdin(batch))
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	return err
}
