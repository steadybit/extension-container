// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package diskfill

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"github.com/steadybit/extension-kit/extutil"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type DiskFill struct {
	startBundle runc.ContainerBundle
	sizeBundle  runc.ContainerBundle
	runc        runc.Runc

	ddCond   *sync.Cond
	rmCond   *sync.Cond
	ddExited bool
	rmExited bool
	err      error
	args     []string
}

const MaxBlockSize = 1024 //Megabytes (1GB)
const cGroupChild = "disk-fill"
const mountPoint = "/disk-fill-temp"

var counter = atomic.Int32{}

type Opts struct {
	BlockSize int    // in megabytes
	Size      int    // in megabytes or percentage
	Mode      string // PERCENTAGE or MB_TO_FILL or MB_LEFT
	TempPath  string
}

func (o *Opts) DDArgs(tempPath string, blockSize int, writeKBytes int) []string {
	args := []string{"if=/dev/zero"}
	args = append(args, "of="+tempPath+"/disk-fill")
	args = append(args, fmt.Sprintf("bs=%vK", +blockSize))
	args = append(args, fmt.Sprintf("count=%v", strconv.Itoa(writeKBytes/blockSize)))

	if log.Trace().Enabled() {
		args = append(args, "status=progress")
	}
	return args
}

func (o *Opts) RmArgs(tempPath string) []string {
	args := []string{"-rf", tempPath + "/disk-fill"}

	if log.Trace().Enabled() {
		args = append(args, "-v")
	}
	return args
}

func New(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) (*DiskFill, error) {
	//calculate size to fill
	neededKiloBytesToWrite := 0
	var sizeBundle runc.ContainerBundle
	sizeInKB := opts.Size * 1024
	if opts.Mode == "MB_TO_FILL" {
		neededKiloBytesToWrite = sizeInKB
	} else if opts.Mode == "PERCENTAGE" || opts.Mode == "MB_LEFT" {
		var space *space
		var err error
		space, sizeBundle, err = resolveDiskSpace(ctx, r, config, opts)
		if err != nil {
			log.Error().Err(err).Msg("failed to resolve disk space")
			return nil, err
		}
		if opts.Mode == "PERCENTAGE" {
			neededKiloBytesToWrite = space.capacity * sizeInKB / 100
		} else { // MB_LEFT
			neededKiloBytesToWrite = space.available - sizeInKB
		}
	} else {
		log.Error().Msgf("Invalid size unit %s", opts.Mode)
		return nil, fmt.Errorf("invalid size unit %s", opts.Mode)
	}

	var startBundle runc.ContainerBundle
	var ddProcessArgs []string
	var err error
	blockSizeInKB := opts.BlockSize * 1024
	if neededKiloBytesToWrite > 0 {
		if blockSizeInKB > (MaxBlockSize * 1024) {
			log.Debug().Msgf("block size %v is bigger than max block size %v", blockSizeInKB, MaxBlockSize*1024)
			blockSizeInKB = MaxBlockSize * 1024
			log.Debug().Msgf("setting block size to %v", blockSizeInKB)
		}
		if blockSizeInKB > neededKiloBytesToWrite {
			log.Debug().Msgf("block size %v is bigger than needed size %v", blockSizeInKB, neededKiloBytesToWrite)
			if neededKiloBytesToWrite > (1024 * 1024) {
				blockSizeInKB = 1024 * 1024
			} else {
				blockSizeInKB = neededKiloBytesToWrite
			}
			log.Debug().Msgf("setting block size to %v", blockSizeInKB)
		}

		//create start bundle
		startId := getNextContainerId(config.ContainerID)
		startBundle, err = CreateBundle(ctx, r, config, startId, opts.TempPath, func(tempPath string) []string {
			ddProcessArgs = append([]string{"dd"}, opts.DDArgs(tempPath, blockSizeInKB, neededKiloBytesToWrite)...)
			return ddProcessArgs
		}, cGroupChild, mountPoint)
		if err != nil {
			log.Error().Err(err).Msg("failed to create start bundle")
			return nil, err
		}
	}

	return &DiskFill{
		startBundle: startBundle,
		sizeBundle:  sizeBundle,
		runc:        r,
		ddCond:      sync.NewCond(&sync.Mutex{}),
		rmCond:      sync.NewCond(&sync.Mutex{}),
		args:        ddProcessArgs,
	}, nil
}

func resolveDiskSpace(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) (*space, runc.ContainerBundle, error) {
	sizeId := getNextContainerId(config.ContainerID)
	sizeBundle, err := CreateBundle(ctx, r, config, sizeId, opts.TempPath, func(tempPath string) []string {
		return []string{"df", "-k", tempPath}
	}, cGroupChild, mountPoint)
	if err != nil {
		log.Error().Err(err).Msg("failed to create calculate size bundle")
		return nil, nil, err
	}
	// run df bundle
	dfResult, err := runc.RunBundleAndWait(context.Background(), r, sizeBundle, "df")
	if err != nil {
		log.Error().Err(err).Msg("failed to measure disk size")
		return nil, nil, err
	}
	diskUsage, err := calculateDiskUsage(dfResult)
	if err != nil {
		log.Warn().Err(err).Msg("failed to calculate disk usage")
		return nil, nil, err
	}
	log.Trace().Msgf("Disk usage: %v", diskUsage)
	return extutil.Ptr(diskUsage), sizeBundle, nil
}

func getNextContainerId(targetId string) string {
	return fmt.Sprintf("sb-disk-fill-%d-%s", counter.Add(1), targetId[:8])
}

func (df *DiskFill) Exited() (bool, error) {
	df.ddCond.L.Lock()
	defer df.ddCond.L.Unlock()
	return df.ddExited, df.err
}

func (df *DiskFill) Args() []string {
	return df.args
}

func (df *DiskFill) HasSomethingToDo() bool {
	return df.startBundle != nil
}

func (df *DiskFill) Start() error {
	log.Info().
		Str("targetContainer", df.startBundle.ContainerId()).
		Strs("args", df.args).
		Msg("Starting dd")
	err := runc.RunBundle(context.Background(), df.runc, df.startBundle, df.ddCond, &df.ddExited, &df.err, "dd")
	if err != nil {
		return fmt.Errorf("failed to start dd: %w", err)
	}
	return nil
}

func (df *DiskFill) Stop(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, opts Opts) error {
	log.Info().
		Str("targetContainer", df.startBundle.ContainerId()).
		Msg("removing dd file")

	//create stop bundle
	stopId := getNextContainerId(config.ContainerID)
	stopBundle, err := CreateBundle(ctx, r, config, stopId, opts.TempPath, func(tempPath string) []string {
		return append([]string{"rm"}, opts.RmArgs(tempPath)...)
	}, cGroupChild, mountPoint)
	if err != nil {
		log.Error().Err(err).Msg("failed to create bundle")
		return err
	}
	// run stop bundle
	err = runc.RunBundle(context.Background(), df.runc, stopBundle, df.rmCond, &df.rmExited, &df.err, "rm")
	if err != nil {
		log.Warn().Err(err).Msg("failed to remove dd file")
	}
	df.wait()
	if err := df.runc.Kill(ctx, df.startBundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", df.startBundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timerStart := time.AfterFunc(10*time.Second, func() {
		if err := df.runc.Kill(ctx, df.startBundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", df.startBundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	if err := df.runc.Kill(ctx, stopBundle.ContainerId(), syscall.SIGINT); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to send SIGINT to container")
	}

	timerStop := time.AfterFunc(10*time.Second, func() {
		if err := df.runc.Kill(ctx, stopBundle.ContainerId(), syscall.SIGTERM); err != nil {
			log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to send SIGTERM to container")
		}
	})

	df.wait()
	timerStart.Stop()
	timerStop.Stop()

	if err := df.runc.Delete(ctx, df.startBundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", df.startBundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := df.startBundle.Remove(); err != nil {
		log.Warn().Str("id", df.startBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}

	if err := df.runc.Delete(ctx, stopBundle.ContainerId(), false); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to delete container")
	}

	if err := stopBundle.Remove(); err != nil {
		log.Warn().Str("id", stopBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
	}

	if df.sizeBundle != nil {
		if err := df.runc.Delete(ctx, df.sizeBundle.ContainerId(), false); err != nil {
			log.Warn().Str("id", df.sizeBundle.ContainerId()).Err(err).Msg("failed to delete container")
		}

		if err := df.sizeBundle.Remove(); err != nil {
			log.Warn().Str("id", df.sizeBundle.ContainerId()).Err(err).Msg("failed to remove bundle")
		}
	}
	return nil
}

func (df *DiskFill) wait() {
	df.ddCond.L.Lock()
	defer df.ddCond.L.Unlock()
	df.rmCond.L.Lock()
	defer df.rmCond.L.Unlock()
	if !df.ddExited {
		df.ddCond.Wait()
	}
	if !df.rmExited {
		df.rmCond.Wait()
	}
}
