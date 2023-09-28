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
	Runc runc.Runc
	Cfg  TargetContainerConfig
}

func (d RuncDigRunner) Run(ctx context.Context, arg []string, stdin io.Reader) ([]byte, error) {
	defer trace.StartRegion(ctx, "RuncDigRunner.Run").End()
	id := getNextContainerId(d.Cfg.ContainerID)

	bundle, cleanup, err := d.Runc.PrepareBundle(ctx, utils.SidecarImagePath(), id)
	defer func() { _ = cleanup() }()
	if err != nil {
		return nil, err
	}

	if err := utils.CopyFileFromProcessToBundle(ctx, bundle, d.Cfg.Pid, "/etc/resolv.conf"); err != nil {
		log.Warn().Err(err).Msg("could not copy /etc/resolv.conf")
	}

	if err := utils.CopyFileFromProcessToBundle(ctx, bundle, d.Cfg.Pid, "/etc/hosts"); err != nil {
		log.Warn().Err(err).Msg("could not copy /etc/hosts")
	}

	if err = d.Runc.EditSpec(
		ctx,
		bundle,
		runc.WithHostname(fmt.Sprintf("dig-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithSelectedNamespaces(utils.ResolveNamespacesUsingInode(ctx, d.Cfg.Namespaces), specs.NetworkNamespace),
		runc.WithCapabilities("CAP_NET_ADMIN"),
		runc.WithProcessArgs(append([]string{"dig"}, arg...)...),
	); err != nil {
		return nil, err
	}

	var outb, errb bytes.Buffer
	err = d.Runc.Run(ctx, id, bundle, runc.IoOpts{Stdin: stdin, Stdout: &outb, Stderr: &errb})
	defer func() { _ = d.Runc.Delete(context.Background(), id, true) }()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, errb.String())
	}
	return outb.Bytes(), nil
}
