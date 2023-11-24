// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package stress

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Stress struct {
	bundle runc.ContainerBundle
	runc   runc.Runc

	cond   *sync.Cond
	exited bool
	err    error
	args   []string
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

	processArgs := append([]string{"stress-ng"}, opts.Args()...)
	bundle, err := runc.CreateBundle(ctx, r, config, id, opts.TempPath, processArgs, "stress", "/stress-temp")

	if err != nil {
		return nil, fmt.Errorf("failed to prepare bundle: %w", err)
	}

	return &Stress{
		bundle: bundle,
		runc:   r,
		cond:   sync.NewCond(&sync.Mutex{}),
		args:   processArgs,
	}, nil
}

func getNextContainerId(targetId string) string {
	return fmt.Sprintf("sb-stress-%d-%s", counter.Add(1), targetId[:8])
}

func (s *Stress) Exited() (bool, error) {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	return s.exited, s.err
}

func (s *Stress) Start() error {
	log.Info().
		Str("targetContainer", s.bundle.ContainerId()).
		Strs("args", s.args).
		Msg("Starting stress-ng")

	err := runc.RunBundle(s.runc, s.bundle, s.cond, &s.exited, &s.err, "stress-ng")
	if err != nil {
		return fmt.Errorf("failed to start stress-ng: %w", err)
	}
	return nil
}

func (s *Stress) Stop() {
	log.Info().
		Str("targetContainer", s.bundle.ContainerId()).
		Msg("Stopping stress-ng")

	ctx := context.Background()
	if err := s.runc.Kill(ctx, s.bundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", s.bundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timer := time.AfterFunc(10*time.Second, func() {
		if err := s.runc.Kill(ctx, s.bundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", s.bundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	s.wait()
	timer.Stop()

	if err := s.runc.Delete(ctx, s.bundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", s.bundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := s.bundle.Remove(); err != nil {
		log.Warn().Str("id", s.bundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}
}

func (s *Stress) wait() {
	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	if !s.exited {
		s.cond.Wait()
	}
}
