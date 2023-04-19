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
	"io"
	"sync/atomic"
)

var counter = atomic.Int32{}

func Apply(ctx context.Context, r runc.Runc, targetId string, opts Opts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("applying network config")

	return generateAndRunCommands(ctx, r, targetId, opts, ModeAdd)
}

func generateAndRunCommands(ctx context.Context, r runc.Runc, targetId string, opts Opts, mode Mode) error {
	ipCommandsV4, err := opts.IpCommands(FamilyV4, mode)
	if err != nil {
		return err
	}

	ipCommandsV6, err := opts.IpCommands(FamilyV6, mode)
	if err != nil {
		return err
	}

	tcCommands, err := opts.TcCommands(mode)
	if err != nil {
		return err
	}

	if ipCommandsV4 != nil {
		err = executeIpCommands(ctx, r, targetId, FamilyV4, ipCommandsV4)
		if err != nil {
			return err
		}
	}

	if ipCommandsV6 != nil {
		err = executeIpCommands(ctx, r, targetId, FamilyV6, ipCommandsV6)
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

func Revert(ctx context.Context, r runc.Runc, targetId string, opts Opts) error {
	log.Info().
		Str("targetContainer", targetId).
		Msg("reverting network config")

	return generateAndRunCommands(ctx, r, targetId, opts, ModeDelete)

}

func getNextContainerId() string {
	return fmt.Sprintf("sb-network-%d", counter.Add(1))
}

func executeIpCommands(ctx context.Context, r runc.Runc, targetId string, family Family, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	id := getNextContainerId()
	bundle, cleanup, err := createBundleAndSpec(ctx, r, id, targetId, func(spec *specs.Spec) {
		spec.Process.Args = []string{"ip", "-family", string(family), "-force", "-batch", "-"}
	})
	if err != nil {
		_ = cleanup()
		return err
	}

	err = r.Run(ctx, id, bundle, runc.InheritStdIo().WithStdin(batch))
	defer func() {
		_ = cleanup()
		_ = r.Delete(context.Background(), id, true)
	}()
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
	if err != nil {
		_ = cleanup()
		return err
	}

	err = r.Run(ctx, id, bundle, runc.InheritStdIo().WithStdin(batch))
	defer func() {
		_ = cleanup()
		_ = r.Delete(context.Background(), id, true)
	}()
	return err
}

func createBundleAndSpec(ctx context.Context, r runc.Runc, id, targetId string, editFn runc.SpecEditor) (string, func() error, error) {
	state, err := r.State(ctx, targetId)
	if err != nil {
		return "", nil, fmt.Errorf("could not load state of target container: %w", err)
	}

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

	bundle, cleanupBundle, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	finalizers = append(finalizers, cleanupBundle)
	if err != nil {
		_ = cleanup()
		return "", nil, err
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
		return "", nil, err
	}

	unmount, err := runc.MountFileOf(ctx, bundle, state.Pid, "/etc/hosts")
	finalizers = append(finalizers, unmount)
	if err != nil {
		_ = cleanup()
		return "", nil, err
	}

	unmount, err = runc.MountFileOf(ctx, bundle, state.Pid, "/etc/resolv.conf")
	finalizers = append(finalizers, unmount)
	if err != nil {
		_ = cleanup()
		return "", nil, err
	}

	return bundle, cleanup, nil
}
