// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package diskfill

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
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
	runc        runc.Runc

	ddCond   *sync.Cond
	rmCond   *sync.Cond
	ddExited bool
	rmExited bool
	err      error
	args     []string
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
	args := []string{"-rf", o.TempPath + "/disk-fill"}

	if log.Trace().Enabled() {
		args = append(args, "-v")
	}
	return args
}

func New(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) (*DiskFill, error) {
	startId := getNextContainerId(config.ContainerID)

	//create start bundle
	ddSuccess := false

	startBundle, err := r.Create(ctx, utils.SidecarImagePath(), startId)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare bundle: %w", err)
	}
	defer func() {
		if !ddSuccess {
			if err := startBundle.Remove(); err != nil {
				log.Warn().Str("id", startId).Err(err).Msg("failed to remove bundle")
			}
		}
	}()

	if opts.TempPath != "" {
		if err := startBundle.MountFromProcess(ctx, config.Pid, opts.TempPath, "/disk-fill-temp"); err != nil {
			log.Warn().Err(err).Msgf("failed to mount %s", opts.TempPath)
		} else {
			opts.TempPath = "/disk-fill-temp"
		}
	}

	ddProcessArgs := append([]string{"dd"}, opts.DDArgs()...)
	if err := startBundle.EditSpec(ctx,
		runc.WithHostname(startId),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithProcessArgs(ddProcessArgs...),
		runc.WithProcessCwd("/tmp"),
		runc.WithCgroupPath(config.CGroupPath, "disk-fill"),
		runc.WithNamespaces(utils.ToLinuxNamespaces(utils.FilterNamespaces(config.Namespaces, specs.PIDNamespace))),
		runc.WithCapabilities("CAP_SYS_RESOURCE"),
		runc.WithMountIfNotPresent(specs.Mount{
			Destination: "/tmp",
			Type:        "tmpfs",
			Options:     []string{"noexec", "nosuid", "nodev", "rprivate"},
		}),
	); err != nil {
		return nil, fmt.Errorf("failed to create config.json: %w", err)
	}

	ddSuccess = true

	return &DiskFill{
		startBundle: startBundle,
		runc:        r,
		ddCond:      sync.NewCond(&sync.Mutex{}),
		rmCond:      sync.NewCond(&sync.Mutex{}),
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
	err := runc.RunBundle(context.Background(), s.runc, s.startBundle, s.ddCond, &s.ddExited, &s.err, "dd")
	if err != nil {
		return fmt.Errorf("failed to start dd: %w", err)
	}
	return nil
}

func (s *DiskFill) Stop(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) error {
	log.Info().
		Str("targetContainer", s.startBundle.ContainerId()).
		Msg("removing dd file")

	//create stop bundle
	stopId := getNextContainerId(config.ContainerID)
	rmSuccess := false

	stopBundle, err := r.Create(ctx, utils.SidecarImagePath(), stopId)
	if err != nil {
		return fmt.Errorf("failed to prepare bundle: %w", err)
	}
	defer func() {
		if !rmSuccess {
			if err := stopBundle.Remove(); err != nil {
				log.Warn().Str("id", stopId).Err(err).Msg("failed to remove bundle")
			}
		}
	}()

	if opts.TempPath != "" {
		if err := stopBundle.MountFromProcess(ctx, config.Pid, opts.TempPath, "/disk-fill-temp"); err != nil {
			log.Warn().Err(err).Msgf("failed to mount %s", opts.TempPath)
		} else {
			opts.TempPath = "/disk-fill-temp"
		}
	}

	rmProcessArgs := append([]string{"rm"}, opts.RmArgs()...)
	if err := stopBundle.EditSpec(ctx,
		runc.WithHostname(stopId),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithProcessArgs(rmProcessArgs...),
		runc.WithProcessCwd("/tmp"),
		runc.WithCgroupPath(config.CGroupPath, "disk-fill"),
		runc.WithNamespaces(utils.ToLinuxNamespaces(utils.FilterNamespaces(config.Namespaces, specs.PIDNamespace))),
		runc.WithCapabilities("CAP_SYS_RESOURCE"),
		runc.WithMountIfNotPresent(specs.Mount{
			Destination: "/tmp",
			Type:        "tmpfs",
			Options:     []string{"noexec", "nosuid", "nodev", "rprivate"},
		}),
	); err != nil {
		return fmt.Errorf("failed to create config.json: %w", err)
	}

	rmSuccess = true

	err = runc.RunBundle(context.Background(), s.runc, stopBundle, s.rmCond, &s.rmExited, &s.err, "rm")
	if err != nil {
		log.Warn().Err(err).Msg("failed to remove dd file")
	}
	s.wait()
	if err := s.runc.Kill(ctx, s.startBundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timerStart := time.AfterFunc(10*time.Second, func() {
		if err := s.runc.Kill(ctx, s.startBundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	if err := s.runc.Kill(ctx, stopBundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timerStop := time.AfterFunc(10*time.Second, func() {
		if err := s.runc.Kill(ctx, stopBundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	s.wait()
	timerStart.Stop()
	timerStop.Stop()

	if err := s.runc.Delete(ctx, s.startBundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := s.startBundle.Remove(); err != nil {
		log.Warn().Str("id", s.startBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}

	if err := s.runc.Delete(ctx, stopBundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := stopBundle.Remove(); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}
	return nil
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
