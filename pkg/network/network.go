// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/networkutils"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"github.com/steadybit/extension-kit/extutil"
	"strconv"
	"sync/atomic"
)

var (
	counter = atomic.Int32{}
	runLock = utils.NewHashedKeyMutex(10)

	sidecarImagePath = utils.SidecarImagePath
)

type TargetContainerConfig struct {
	ContainerID string                          `json:"id"`
	Pid         int                             `json:"pid"`
	Namespaces  []utils.LinuxNamespaceWithInode `json:"namespaces"`
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
		Str("containerId", config.ContainerID).
		Msg("applying network config")

	if err := utils.CheckNamespacesExists(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace); err != nil {
		return fmt.Errorf("container exited? %w", err)
	}

	return generateAndRunCommands(ctx, r, config, opts, networkutils.ModeAdd)
}

func Revert(ctx context.Context, r runc.Runc, config TargetContainerConfig, opts networkutils.Opts) (action_kit_api.Messages, error) {

	if err := utils.CheckNamespacesExists(config.Namespaces, specs.NetworkNamespace, specs.UTSNamespace); err != nil {
		log.Info().
			Str("containerId", config.ContainerID).
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
		Str("containerd", config.ContainerID).
		Msg("reverting network config")

	return nil, generateAndRunCommands(ctx, r, config, opts, networkutils.ModeDelete)
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

	netNsID := getNetworkNs(config.Namespaces)
	runLock.LockKey(netNsID)
	defer func() { _ = runLock.UnlockKey(netNsID) }()

	if ipCommandsV4 != nil {
		if ipErr := executeIpCommands(ctx, r, config, networkutils.FamilyV4, ipCommandsV4); ipErr != nil {
			err = errors.Join(err, networkutils.FilterBatchErrors(ipErr, mode, ipCommandsV4))
		}
	}

	if ipCommandsV6 != nil {
		if ipErr := executeIpCommands(ctx, r, config, networkutils.FamilyV6, ipCommandsV6); ipErr != nil {
			err = errors.Join(err, networkutils.FilterBatchErrors(ipErr, mode, ipCommandsV6))
		}
	}

	if tcCommands != nil {
		if tcErr := executeTcCommands(ctx, r, config, tcCommands); tcErr != nil {
			err = errors.Join(err, networkutils.FilterBatchErrors(tcErr, mode, tcCommands))
		}
	}

	return err
}

func getNetworkNs(namespaces []utils.LinuxNamespaceWithInode) string {
	for _, ns := range namespaces {
		if ns.Type == specs.NetworkNamespace {
			if ns.Inode != 0 {
				return strconv.FormatUint(ns.Inode, 10)
			} else {
				return ns.Path
			}
		}
	}
	return ""
}

func getNextContainerId(targedId string) string {
	l := 8
	if len(targedId) < l {
		l = len(targedId)
	}
	return fmt.Sprintf("sb-network-%d-%s", counter.Add(1), targedId[:l])
}

func executeIpCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, family networkutils.Family, cmds []string) error {
	if len(cmds) == 0 {
		return nil
	}

	id := getNextContainerId(config.ContainerID)
	bundle, cleanup, err := r.PrepareBundle(ctx, sidecarImagePath(), id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	cmd := []string{"ip", "-family", string(family), "-force", "-batch", "-"}

	if err = r.EditSpec(
		bundle,
		runc.WithHostname(fmt.Sprintf("ip-%s", id)),
		runc.WithAnnotations(map[string]string{"com.steadybit.sidecar": "true"}),
		runc.WithSelectedNamespaces(utils.ResolveNamespacesUsingInode(config.Namespaces), specs.NetworkNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs(cmd...),
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
		if parsed := networkutils.ParseBatchError(cmd, bytes.NewReader(outb.Bytes())); parsed != nil {
			return parsed
		}
		return fmt.Errorf("%s ip failed: %w, output: %s", id, err, outb.String())
	}
	return nil
}

func executeTcCommands(ctx context.Context, r runc.Runc, config TargetContainerConfig, cmds []string) error {
	if len(cmds) == 0 {
		return nil
	}

	id := getNextContainerId(config.ContainerID)
	bundle, cleanup, err := r.PrepareBundle(ctx, sidecarImagePath(), id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return err
	}

	cmd := []string{"tc", "-force", "-batch", "-"}
	if err = r.EditSpec(
		bundle,
		runc.WithHostname(fmt.Sprintf("tc-%s", id)),
		runc.WithAnnotations(map[string]string{"com.steadybit.sidecar": "true"}),
		runc.WithSelectedNamespaces(utils.ResolveNamespacesUsingInode(config.Namespaces), specs.NetworkNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs(cmd...),
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
		if parsed := networkutils.ParseBatchError(cmd, bytes.NewReader(outb.Bytes())); parsed != nil {
			return parsed
		}
		return fmt.Errorf("%s tc failed: %w, output: %s", id, err, outb.String())
	}
	return nil
}
