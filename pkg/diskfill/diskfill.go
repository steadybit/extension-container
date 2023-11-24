// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package diskfill

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

type DiskFill struct {
	startBundle runc.ContainerBundle
	stopBundle runc.ContainerBundle
	runc   runc.Runc

	ddCond   *sync.Cond
	rmCond   *sync.Cond
	ddExited bool
	rmExited bool
	err      error
	args   []string
}

var counter = atomic.Int32{}

type Opts struct {
	BlockSize  int // in kilobytes
	SizeToFill int // in kilobytes
	TempPath   string
}

func (o *Opts) DDArgs() []string {
	args := []string{"if=/dev/urandom"}
	args = append(args, "of="+o.TempPath+"/disk-fill")
	args = append(args, fmt.Sprintf("bs=%vK", +o.BlockSize))
	args = append(args, fmt.Sprintf("count=%v", strconv.Itoa(o.SizeToFill/o.BlockSize)))

	if log.Trace().Enabled() {
		args = append(args, "status=progress")
	}
	return args
}

func (o *Opts) RmArgs() []string {
	args := []string{"-rf", o.TempPath+"/disk-fill"}

	if log.Trace().Enabled() {
		args = append(args, "-v")
	}
	return args
}

func New(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) (*DiskFill, error) {
	id := getNextContainerId(config.ContainerID)

	ddProcessArgs := append([]string{"dd"}, opts.DDArgs()...)
	startBundle, err := runc.CreateBundle(ctx, r, config, id, opts.TempPath, ddProcessArgs, "disk-fill", "/disk-fill-temp")

	if err != nil {
		return nil, fmt.Errorf("failed to create startBundle: %w", err)
	}

	rmProcessArgs := append([]string{"rm"}, opts.RmArgs()...)
	stopBundle, err := runc.CreateBundle(ctx, r, config, id, opts.TempPath, rmProcessArgs, "disk-fill", "/disk-fill-temp")

	if err != nil {
		return nil, fmt.Errorf("failed to create stopBundle: %w", err)
	}

	return &DiskFill{
		startBundle: startBundle,
		stopBundle: stopBundle,
		runc:        r,
		ddCond:      sync.NewCond(&sync.Mutex{}),
		args:        ddProcessArgs,
	}, nil
}

func getNextContainerId(targetId string) string {
	return fmt.Sprintf("sb-disk-fill-%d-%s", counter.Add(1), targetId[:8])
}

func (s *DiskFill) Exited() (bool, error) {
	s.ddCond.L.Lock()
	defer s.ddCond.L.Unlock()
	return s.ddExited, s.err
}

func (s *DiskFill) Start() error {
	log.Info().
		Str("targetContainer", s.startBundle.ContainerId()).
		Strs("args", s.args).
		Msg("Starting dd")
	err := runc.RunBundle(s.runc, s.startBundle, s.ddCond, &s.ddExited, &s.err, "dd")
	if err != nil {
		return fmt.Errorf("failed to start dd: %w", err)
	}
	return nil
}

func (s *DiskFill) Stop() {
	log.Info().
		Str("targetContainer", s.startBundle.ContainerId()).
		Msg("removing dd file")

	err := runc.RunBundle(s.runc, s.stopBundle, s.rmCond, &s.rmExited, &s.err, "rm")
	if err != nil {
		log.Warn().Err(err).Msg("failed to remove dd file")
	}

	ctx := context.Background()
	if err := s.runc.Kill(ctx, s.startBundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timer := time.AfterFunc(10*time.Second, func() {
		if err := s.runc.Kill(ctx, s.startBundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	s.wait()
	timer.Stop()

	if err := s.runc.Delete(ctx, s.startBundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := s.startBundle.Remove(); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}
}

func (s *DiskFill) wait() {
	s.ddCond.L.Lock()
	defer s.ddCond.L.Unlock()
	s.rmCond.L.Lock()
	defer s.rmCond.L.Unlock()
	if !s.ddExited {
		s.ddCond.Wait()
	}
	if !s.rmExited {
		s.rmCond.Wait()
	}
}
