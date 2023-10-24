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
	"io"
	"runtime/trace"
)

type RuncDigRunner struct {
	Runc   runc.Runc
	Config utils.TargetContainerConfig
}

func (r *RuncDigRunner) Run(ctx context.Context, arg []string, stdin io.Reader) ([]byte, error) {
	defer trace.StartRegion(ctx, "RuncDigRunner.Run").End()
	id := getNextContainerId(r.Config.ContainerID)

	bundle, err := r.Runc.Create(ctx, utils.SidecarImagePath(), id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := bundle.Remove(); err != nil {
			log.Warn().Str("id", id).Err(err).Msg("failed to remove bundle")
		}
	}()

	if err := bundle.CopyFileFromProcess(ctx, r.Config.Pid, "/etc/resolv.conf", "/etc/resolv.conf"); err != nil {
		log.Warn().Err(err).Msg("failed to copy /etc/resolv.conf")
	}

	if err := bundle.CopyFileFromProcess(ctx, r.Config.Pid, "/etc/hosts", "/etc/hosts"); err != nil {
		log.Warn().Err(err).Msg("failed to copy /etc/hosts")
	}

	namespaces := utils.FilterNamespaces(r.Config.Namespaces, []specs.LinuxNamespaceType{specs.NetworkNamespace}...)
	utils.RefreshNamespacesUsingInode(ctx, namespaces)

	if err = bundle.EditSpec(
		ctx,
		runc.WithHostname(fmt.Sprintf("dig-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithNamespaces(utils.ToLinuxNamespaces(namespaces)),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs(append([]string{"dig"}, arg...)...),
	); err != nil {
		return nil, err
	}

	var outb, errb bytes.Buffer
	err = r.Runc.Run(ctx, bundle, runc.IoOpts{Stdin: stdin, Stdout: &outb, Stderr: &errb})
	defer func() {
		if err := r.Runc.Delete(context.Background(), id, true); err != nil {
			log.Warn().Str("id", id).Err(err).Msg("failed to delete container")
		}
	}()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, errb.String())
	}
	return outb.Bytes(), nil
}
