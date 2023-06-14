// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package stress

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
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

type StressOpts struct {
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

func (o *StressOpts) Args() []string {
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

func New(r runc.Runc, targetId string, opts StressOpts) (*Stress, error) {
	ctx := context.Background()

	state, err := r.State(ctx, targetId)
	if err != nil {
		return nil, fmt.Errorf("could not load state of target container: %w", err)
	}

	id := getNextContainerId()
	bundle, cleanupBundle, err := r.PrepareBundle(ctx, "sidecar.tar", id)
	if err != nil {
		return nil, err
	}

	cgroupPath, err := utils.ReadCgroupPath(state.Pid)
	if err != nil {
		return nil, err
	}

	namespaces, err := utils.ReadNamespaces(state.Pid)
	if err != nil {
		return nil, err
	}

	if err := runc.EditSpec(bundle,
		runc.WithHostname(fmt.Sprintf("stress-ng-%s", id)),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithProcessArgs(append([]string{"stress-ng"}, opts.Args()...)...),
		runc.WithProcessCwd("/tmp"),
		runc.WithCgroupPath(cgroupPath, "stress"),
		runc.WithSelectedNamespaces(namespaces, specs.PIDNamespace, specs.UTSNamespace),
		runc.WithCapabilities("CAP_SYS_RESOURCE"),
		runc.WithMountIfNotPresent(specs.Mount{
			Destination: "/tmp",
			Type:        "tmpfs",
			Options:     []string{"noexec", "nosuid", "nodev", "rprivate"},
		}),
	); err != nil {
		return nil, err
	}

	wait := make(chan error)
	ctx, cancel := context.WithCancel(context.Background())
	start := func() error {
		log.Info().
			Str("targetContainer", targetId).
			Strs("args", opts.Args()).
			Msg("Starting stress-ng")
		go func() {
			wait <- r.Run(ctx, id, bundle, runc.InheritStdIo())
		}()
		return nil
	}

	stop := func() {
		log.Info().
			Str("targetContainer", targetId).
			Msg("Stopping stress-ng")
		cancel()
		_ = r.Delete(context.Background(), id, true)
		_ = cleanupBundle()
	}

	return &Stress{
		start: start,
		stop:  stop,
		err:   wait,
	}, nil
}

func getNextContainerId() string {
	return fmt.Sprintf("sb-stress-%d", counter.Add(1))
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
