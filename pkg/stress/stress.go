// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package stress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"os/exec"
	"strconv"
	"sync/atomic"
	"time"
)

type Stress struct {
	stop  func()
	start func() error
	err   chan error
}

var counter = atomic.Int32{}

type Opts struct {
	CpuWorkers *int
	CpuLoad    int
	HddWorkers *int
	HddBytes   string
	IoWorkers  *int
	TempPath   string
	Timeout    time.Duration
	VmWorkers  *int
	VmHang     time.Duration
	VmBytes    string
}

func (o *Opts) Args() []string {
	args := []string{"--timeout", strconv.Itoa(int(o.Timeout.Seconds()))}
	if o.CpuWorkers != nil {
		args = append(args, "--cpu", strconv.Itoa(*o.CpuWorkers), "--cpu-load", strconv.Itoa(o.CpuLoad))
	}
	if o.HddWorkers != nil {
		args = append(args, "--hdd", strconv.Itoa(*o.HddWorkers), "--hdd-bytes", o.HddBytes)
	}
	if o.IoWorkers != nil {
		args = append(args, "--io", strconv.Itoa(*o.IoWorkers))
	}
	if o.TempPath != "" {
		args = append(args, "--temp-path", o.TempPath)
	}
	if o.VmWorkers != nil {
		args = append(args, "--vm", strconv.Itoa(*o.VmWorkers), "--vm-bytes", o.VmBytes, "--vm-hang", "0")
	}
	if log.Trace().Enabled() {
		args = append(args, "-v")
	}
	return args
}

func New(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) (*Stress, error) {
	id := getNextContainerId(config.ContainerID)
	success := false

	bundle, err := r.Create(ctx, utils.SidecarImagePath(), id)
	if err != nil {
		return nil, fmt.Errorf("could not prepare bundle: %w", err)
	}
	defer func() {
		if !success {
			if err := bundle.Remove(); err != nil {
				log.Warn().Str("id", id).Err(err).Msg("could not remove bundle")
			}
		}
	}()

	if opts.TempPath != "" {
		if err := bundle.MountFromProcess(ctx, config.Pid, opts.TempPath, "/stress-temp"); err != nil {
			log.Warn().Err(err).Msgf("could not mount %s", opts.TempPath)
		} else {
			opts.TempPath = "/stress-temp"
		}
	}

	if err := bundle.EditSpec(ctx,
		runc.WithHostname(id),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithProcessArgs(append([]string{"stress-ng"}, opts.Args()...)...),
		runc.WithProcessCwd("/tmp"),
		runc.WithCgroupPath(config.CGroupPath, "stress"),
		runc.WithNamespaces(utils.ToLinuxNamespaces(utils.FilterNamespaces(config.Namespaces, specs.PIDNamespace))),
		runc.WithCapabilities("CAP_SYS_RESOURCE"),
		runc.WithMountIfNotPresent(specs.Mount{
			Destination: "/tmp",
			Type:        "tmpfs",
			Options:     []string{"noexec", "nosuid", "nodev", "rprivate"},
		}),
	); err != nil {
		return nil, fmt.Errorf("could not create config.json: %w", err)
	}

	wait := make(chan error)
	ctx, ctxCancel := context.WithCancel(context.Background())
	start := func() error {
		log.Info().
			Str("targetContainer", config.ContainerID).
			Strs("args", opts.Args()).
			Msg("Starting stress-ng")
		go func() {
			var outb bytes.Buffer
			err := r.Run(ctx, bundle, runc.IoOpts{
				Stdin:  nil,
				Stdout: &outb,
				Stderr: &outb,
			})

			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitErr.Stderr = outb.Bytes()
				wait <- exitErr
			}
		}()
		return nil
	}

	stop := func() {
		log.Info().
			Str("targetContainer", config.ContainerID).
			Msg("Stopping stress-ng")

		ctxCancel()

		if err := r.Delete(context.Background(), id, true); err != nil {
			log.Warn().Str("id", id).Err(err).Msg("could not delete container")
		}

		if err := bundle.Remove(); err != nil {
			log.Warn().Str("id", id).Err(err).Msg("could not remove bundle")
		}
	}

	success = true
	return &Stress{
		start: start,
		stop:  stop,
		err:   wait,
	}, nil
}

func getNextContainerId(targetId string) string {
	return fmt.Sprintf("sb-stress-%d-%s", counter.Add(1), targetId[:8])
}

func (s *Stress) Wait() <-chan error {
	return s.err
}

func (s *Stress) Start() error {
	return s.start()
}

func (s *Stress) Stop() {
	s.stop()
}
