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
	"github.com/steadybit/extension-container/pkg/networkutils"
	"github.com/steadybit/extension-container/pkg/utils"
	"io"
	"sync/atomic"
)

var counter = atomic.Int32{}

type TargetContainerConfig struct {
	ContainerID string                 `json:"id"`
	Pid         int                    `json:"pid"`
	Namespaces  []specs.LinuxNamespace `json:"namespaces"`
}

func GetConfigForContainer(ctx context.Context, r runc.Runc, targetId string) (TargetContainerConfig, error) {
	config := TargetContainerConfig{
		ContainerID: targetId,
	}

	state, err := r.State(ctx, targetId)
	if err != nil {
		return config, fmt.Errorf("could not load state of target container: %w", err)
	}
	config.Pid = state.Pid

	namespaces, err := utils.ReadNamespaces(state.Pid)
	if err != nil {
		return config, fmt.Errorf("could not read namespaces of target container: %w", err)
	}
	config.Namespaces = namespaces

	return config, nil
}

func Apply(ctx context.Context, r runc.Runc, config TargetContainerConfig, opts networkutils.Opts) error {
	log.Info().
		Str("config", config.ContainerID).
		Msg("applying network config")

	return generateAndRunCommands(ctx, r, config, opts, networkutils.ModeAdd)
}

func generateAndRunCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, opts networkutils.Opts, mode networkutils.Mode) error {
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
		err = executeIpCommands(ctx, r, config, networkutils.FamilyV4, ipCommandsV4)
		if err != nil {
			return err
		}
	}

	if ipCommandsV6 != nil {
		err = executeIpCommands(ctx, r, config, networkutils.FamilyV6, ipCommandsV6)
		if err != nil {
			return err
		}
	}

	if tcCommands != nil {
		err = executeTcCommands(ctx, r, config, tcCommands)
		if err != nil {
			return err
		}
	}

	return nil
}

func Revert(ctx context.Context, r runc.Runc, config TargetContainerConfig, opts networkutils.Opts) error {
	log.Info().
		Str("config", config.ContainerID).
		Msg("reverting network config")

	return generateAndRunCommands(ctx, r, config, opts, networkutils.ModeDelete)

}

func getNextContainerId() string {
	return fmt.Sprintf("sb-network-%d", counter.Add(1))
}

func executeIpCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, family networkutils.Family, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	id := getNextContainerId()
	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("ip", "-family", string(family), "-force", "-batch", "-"),
	); err != nil {
		return err
	}

	var outb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{
		Stdin:  batch,
		Stdout: &outb,
		Stderr: &outb,
	})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return fmt.Errorf("ip failed: %w, output: %s", err, outb.String())
	}
	return nil
}

func executeTcCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, batch io.Reader) error {
	if batch == nil {
		return nil
	}

	id := getNextContainerId()
	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("tc", "-force", "-batch", "-"),
	); err != nil {
		return err
	}

	var outb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{
		Stdin:  batch,
		Stdout: &outb,
		Stderr: &outb,
	})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return fmt.Errorf("tc failed: %w, output: %s", err, outb.String())
	}
	return nil
}
