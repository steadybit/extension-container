// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"bytes"
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/networkutils"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"github.com/steadybit/extension-kit/extutil"
	"os"
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
		return config, fmt.Errorf("could not read state of target container: %w", err)
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

	if err := checkNamespacesExists(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace); err != nil {
		return fmt.Errorf("container exited? %w", err)
	}

	return generateAndRunCommands(ctx, r, config, opts, networkutils.ModeAdd)
}

func Revert(ctx context.Context, r runc.Runc, config TargetContainerConfig, opts networkutils.Opts) (action_kit_api.Messages, error) {

	if err := checkNamespacesExists(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace); err != nil {
		log.Info().
			Str("config", config.ContainerID).
			AnErr("reason", err).
			Msg("skipping revert network config")

		return []action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Skipped revert network config. Target container %s exited? %s", config.ContainerID, err),
			},
		}, nil
	}

	log.Info().
		Str("config", config.ContainerID).
		Msg("reverting network config")

	return nil, generateAndRunCommands(ctx, r, config, opts, networkutils.ModeDelete)
}

func checkNamespacesExists(namespaces []specs.LinuxNamespace, wantedTypes ...specs.LinuxNamespaceType) error {
	for _, ns := range namespaces {
		wanted := false
		if len(wantedTypes) == 0 {
			wanted = true
		} else {
			for _, wantedType := range wantedTypes {
				if ns.Type == wantedType {
					wanted = true
					break
				}
			}
		}

		if !wanted || ns.Path == "" {
			continue
		}

		if _, err := os.Stat(ns.Path); err != nil && os.IsNotExist(err) {
			return fmt.Errorf("namespace %s doesn't exist", ns.Path)
		}
	}

	return nil
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
			return networkutils.FilterTcBatchErrors(err, mode, tcCommands)
		}
	}

	return nil
}

func getNextContainerId(targedId string) string {
	return fmt.Sprintf("sb-network-%d-%s", counter.Add(1), targedId[:8])
}

func executeIpCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, family networkutils.Family, cmds []string) error {
	if len(cmds) == 0 {
		return nil
	}

	id := getNextContainerId(config.ContainerID)
	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithHostname(fmt.Sprintf("ip-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("ip", "-family", string(family), "-force", "-batch", "-"),
	); err != nil {
		return err
	}

	log.Debug().Strs("cmds", cmds).Str("family", string(family)).Msg("running ip commands")
	var outb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{
		Stdin:  networkutils.ToReader(cmds),
		Stdout: &outb,
		Stderr: &outb,
	})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		return fmt.Errorf("ip failed: %w, output: %s", err, outb.String())
	}
	return nil
}

func executeTcCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, cmds []string) error {
	if len(cmds) == 0 {
		return nil
	}

	id := getNextContainerId(config.ContainerID)
	bundle, cleanup, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	if err = runc.EditSpec(
		bundle,
		runc.WithHostname(fmt.Sprintf("tc-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs("tc", "-force", "-batch", "-"),
	); err != nil {
		return err
	}

	log.Debug().Strs("cmds", cmds).Msg("running tc commands")
	var outb bytes.Buffer
	err = r.Run(ctx, id, bundle, runc.IoOpts{
		Stdin:  networkutils.ToReader(cmds),
		Stdout: &outb,
		Stderr: &outb,
	})
	defer func() { _ = r.Delete(context.Background(), id, true) }()
	if err != nil {
		if parsed := networkutils.ParseTcBatchError(bytes.NewReader(outb.Bytes())); parsed != nil {
			return parsed
		}
		return fmt.Errorf("tc failed: %w, output: %s", err, outb.String())
	}
	return nil
}
