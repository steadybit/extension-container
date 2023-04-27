// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"context"
	"errors"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/networkutils"
	"io"
	"sync/atomic"
)

var counter = atomic.Int32{}

func Apply(ctx context.Context, r runc.Runc, targetId string, opts networkutils.Opts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("applying network config")

	return generateAndRunCommands(ctx, r, targetId, opts, networkutils.ModeAdd)
}

func generateAndRunCommands(ctx context.Context, r runc.Runc, targetId string, opts networkutils.Opts, mode networkutils.Mode) error {
	ipCommandsV4, err := opts.IpCommands(networkutils.FamilyV4, mode)
	if err != nil {
		return err
	}

	ipCommandsV6, err := opts.IpCommands(networkutils.FamilyV6, mode)
	if err != nil {
		return err
	}

	tcCommands, err := opts.TcCommands(mode)
	if err != nil {
		return err
	}

	if ipCommandsV4 != nil {
		err = executeIpCommands(ctx, r, targetId, networkutils.FamilyV4, ipCommandsV4)
		if err != nil {
			return err
		}
	}

	if ipCommandsV6 != nil {
		err = executeIpCommands(ctx, r, targetId, networkutils.FamilyV6, ipCommandsV6)
		if err != nil {
			return err
		}
	}

	if tcCommands != nil {
		err = executeTcCommands(ctx, r, targetId, tcCommands)
		if err != nil {
			return err
		}
	}

	return nil
}

func Revert(ctx context.Context, r runc.Runc, targetId string, opts networkutils.Opts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("reverting network config")

	return generateAndRunCommands(ctx, r, targetId, opts, networkutils.ModeDelete)

}

func getNextContainerId() string {
	return fmt.Sprintf("sb-network-%d", counter.Add(1))
}

func executeIpCommands(ctx context.Context, r runc.Runc, targetId string, family networkutils.Family, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	id := getNextContainerId()
	bundle, cleanup, err := createBundleAndSpec(ctx, r, id, targetId, func(spec *specs.Spec) {
		spec.Process.Args = []string{"ip", "-family", string(family), "-force", "-batch", "-"}
	})
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	err = r.Run(ctx, id, bundle, runc.InheritStdIo().WithStdin(batch))
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	return err
}

func executeTcCommands(ctx context.Context, r runc.Runc, targetId string, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	id := getNextContainerId()
	bundle, cleanup, err := createBundleAndSpec(ctx, r, id, targetId, func(spec *specs.Spec) {
		spec.Process.Args = []string{"tc", "-force", "-batch", "-"}
	})
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	err = r.Run(ctx, id, bundle, runc.InheritStdIo().WithStdin(batch))
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	return err
}

func createBundleAndSpec(ctx context.Context, r runc.Runc, id, targetId string, editFn runc.SpecEditor) (string, func() error, error) {
	var finalizers []func() error
	cleanup := func() error {
		var errs []error
		for _, f := range finalizers {
			if err := f(); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	state, err := r.State(ctx, targetId)
	if err != nil {
		return "", cleanup, fmt.Errorf("could not load state of target container: %w", err)
	}

	bundle, cleanupBundle, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	finalizers = append(finalizers, cleanupBundle)
	if err != nil {
		return "", cleanup, err
	}

	if err := runc.EditSpec(bundle, func(spec *specs.Spec) {
		spec.Hostname = id
		spec.Annotations = map[string]string{
			"com.steadybit.sidecar": "true",
		}
		spec.Root.Path = "rootfs"
		spec.Root.Readonly = true
		spec.Process.Terminal = false
		spec.Process.Cwd = "/tmp"

		runc.AddCapabilities(spec, "CAP_NET_ADMIN")
		runc.UseCgroupOf(spec, state.Pid, "network")
		runc.UseNamespacesOf(spec, state.Pid)

		editFn(spec)
	}); err != nil {
		return "", cleanup, err
	}

	unmount, err := runc.MountFileOf(ctx, bundle, state.Pid, "/etc/hosts")
	finalizers = append(finalizers, unmount)
	if err != nil {
		return "", cleanup, err
	}

	unmount, err = runc.MountFileOf(ctx, bundle, state.Pid, "/etc/resolv.conf")
	finalizers = append(finalizers, unmount)
	if err != nil {
		return "", cleanup, err
	}

	return bundle, cleanup, nil
}
