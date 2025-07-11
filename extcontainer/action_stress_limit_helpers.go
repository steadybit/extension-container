// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2025 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"github.com/kataras/iris/v12/x/mathx"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/action-kit/go/action_kit_commons/utils"
	"github.com/steadybit/extension-kit/extutil"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var cgroupV1MemUnlimited = (math.MaxInt64 / os.Getpagesize()) * os.Getpagesize()
var osFs = osFileSystem{}

func readAndAdaptToContainerLimits(_ context.Context, p ociruntime.LinuxProcessInfo, opts *stress.Opts) {
	cpuLimitInMilliCpu := -1
	memLimitInBytes := -1

	if isCGroupV1() {
		cpuLimitInMilliCpu = readCGroupV1CpuLimit(p.CGroupPath, osFs)
		memLimitInBytes = readCGroupV1MemLimit(p.CGroupPath, osFs)
	} else {
		cpuLimitInMilliCpu = readCGroupV2CpuLimit(p.CGroupPath, osFs)
		memLimitInBytes = readCGroupV2MemLimit(p.CGroupPath, osFs)
	}

	if opts.CpuWorkers != nil {
		if cpuLimitInMilliCpu >= 0 {
			adaptToCpuContainerLimits(cpuLimitInMilliCpu, opts)
		} else if *opts.CpuWorkers == 0 {
			//there might be no limit set but the process to be restricted to certain CPUs or some CPUs programmatically turned off.
			//In this case we need to read for the allowed list of CPUs for the process and pass this to the stress command as stress-ng
			//always uses configured CPUs and not online CPUs
			adaptToAllowedCpus(p.Pid, opts)
		}
	}

	if opts.VmWorkers != nil && memLimitInBytes >= 0 {
		adaptToMemContainerLimits(memLimitInBytes, opts)
	}
}

func adaptToAllowedCpus(pid int, opts *stress.Opts) {
	if cpuCount, err := utils.ReadCpusAllowedCount(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		opts.CpuWorkers = extutil.Ptr(cpuCount)
	} else {
		log.Debug().Err(err).Msg("failed to read cpus_allowed count.")
	}
}

func adaptToMemContainerLimits(memLimitInBytes int, opts *stress.Opts) {
	memConsumptionInPercent := 0
	if _, err := fmt.Sscanf(opts.VmBytes, "%d%%", &memConsumptionInPercent); err != nil {
		log.Warn().Err(err).Msgf("failed to parse memory limit in percent. skip adapting memory consumption to container limits.")
		return
	}

	memConsumptionInBytes := memLimitInBytes * memConsumptionInPercent / 100
	memConsumptionPerWorkerInKBytes := max(memConsumptionInBytes / *opts.VmWorkers / 1024, 1)
	opts.VmBytes = fmt.Sprintf("%dK", memConsumptionPerWorkerInKBytes)

	log.Info().Msgf("container memory limit is %dK. Starting %d workers with memory consumption of %s each", memLimitInBytes/1024, *opts.VmWorkers, opts.VmBytes)
}

func adaptToCpuContainerLimits(cpuLimitInMilliCpu int, opts *stress.Opts) {
	cpuLoadInMillis := cpuLimitInMilliCpu * opts.CpuLoad / 100
	log.Debug().Int("cpuLoad", opts.CpuLoad).Int("cpuLoadInMillis", cpuLoadInMillis).Msg("adapting to container cpu limit")

	if *opts.CpuWorkers == 0 {
		// user didn't specify the number of workers. we start as many workers as we need to reach the desired cpu consumption
		cpuWorkers := int(mathx.RoundUp(float64(cpuLoadInMillis)/1000, 0))
		opts.CpuWorkers = extutil.Ptr(cpuWorkers)
	}

	opts.CpuLoad = int(math.Round(float64(cpuLoadInMillis)/float64(*opts.CpuWorkers)) / 10)
	log.Info().Msgf("container cpu limit is %dm. Starting %d workers with %d%% load.", cpuLimitInMilliCpu, *opts.CpuWorkers, opts.CpuLoad)
}

func isCGroupV1() bool {
	_, err := os.Open("/sys/fs/cgroup/cpu,cpuacct")
	return err == nil
}

type osFileSystem struct{}

func (osFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

type fileSystem interface {
	ReadFile(name string) ([]byte, error)
}

func readCGroupV1CpuLimit(cGroupPath string, fs fileSystem) int {
	cpuCfsQuotaPath := filepath.Join("/sys/fs/cgroup/cpu,cpuacct", cGroupPath, "cpu.cfs_quota_us")
	cpuCfsQuotaRaw, err := fs.ReadFile(cpuCfsQuotaPath)
	if err != nil || len(cpuCfsQuotaRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.cfs_quota_us '%s. skip adapting cpu load to container limits.", cpuCfsQuotaPath)
		return -1
	}

	log.Trace().Msgf("parsing cpu.cfs_quota_us content %s", cpuCfsQuotaRaw)
	cpuCfsQuota, err := strconv.Atoi(strings.Fields(string(cpuCfsQuotaRaw))[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpu.cfs_quota_us: %s. skip adapting cpu load to container limits.", cpuCfsQuotaRaw)
		return -1
	}

	if cpuCfsQuota == -1 {
		log.Debug().Msgf("container cpu is unlimited. skip adapting cpu load to container limits. (cpu.cfs_quota_us=-1)")
		return -1
	}

	cpuCfsPeriodPath := filepath.Join("/sys/fs/cgroup/cpu,cpuacct", cGroupPath, "cpu.cfs_period_us")
	cpuCfsPeriodRaw, err := fs.ReadFile(cpuCfsPeriodPath)
	if err != nil || len(cpuCfsPeriodRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.cfs_period_us '%s. skip adapting cpu load to container limits.", cpuCfsPeriodPath)
		return -1
	}

	log.Trace().Msgf("parsing cpu.cfs_period_us content %s", cpuCfsPeriodRaw)
	cpuCfsPeriod, err := strconv.Atoi(strings.Fields(string(cpuCfsPeriodRaw))[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpu.cfs_period_us: %s. skip adapting cpu load to container limits.", cpuCfsPeriodRaw)
		return -1
	}

	cpuLimitInMilliCpu := cpuCfsQuota * 1000 / cpuCfsPeriod
	log.Debug().Msgf("container cpu limit is %dm", cpuLimitInMilliCpu)
	return cpuLimitInMilliCpu
}

func readCGroupV2CpuLimit(cGroupPath string, fs fileSystem) int {
	cpuMaxCGroupPath := filepath.Join("/sys/fs/cgroup", cGroupPath, "cpu.max")
	cpuMaxCGroupRaw, err := fs.ReadFile(cpuMaxCGroupPath)
	if err != nil || len(cpuMaxCGroupRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.max '%s. skip adapting cpu load to container limits.", cpuMaxCGroupPath)
		return -1
	}

	log.Trace().Msgf("parsing cpu.max content %s", cpuMaxCGroupRaw)
	cpuMaxCGroup := strings.Fields(string(cpuMaxCGroupRaw))
	if len(cpuMaxCGroup) != 2 {
		log.Warn().Msgf("failed to parse cpu.max: %s. skip adapting cpu load to container limits.", cpuMaxCGroupRaw)
		return -1
	} else if cpuMaxCGroup[0] == "max" {
		log.Debug().Msgf("container cpu is unlimited (cpu.max=max ....). skip adapting cpu load to container limits.")
		return -1
	}
	cpuLimitInMicroseconds, err := strconv.Atoi(cpuMaxCGroup[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpuLimitInMicroseconds: %s. skip adapting cpu load to container limits.", cpuMaxCGroup[0])
		return -1
	}
	cpuLimitPeriod, err := strconv.Atoi(cpuMaxCGroup[1])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpuLimitPeriod: %s. skip adapting cpu load to container limits.", cpuMaxCGroup[1])
		return -1
	}
	cpuLimitInMilliCpu := cpuLimitInMicroseconds * 1000 / cpuLimitPeriod
	log.Debug().Msgf("container cpu limit is %dm", cpuLimitInMilliCpu)
	return cpuLimitInMilliCpu
}

func readCGroupV1MemLimit(cGroupPath string, fs fileSystem) int {
	memoryLimitsInBytesPath := filepath.Join("/sys/fs/cgroup/memory", cGroupPath, "memory.limit_in_bytes")
	memoryLimitsInBytesRaw, err := fs.ReadFile(memoryLimitsInBytesPath)
	if err != nil || len(memoryLimitsInBytesRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read memory.limit_in_bytes '%s. skip adapting memory consumption to container limits.", memoryLimitsInBytesPath)
		return -1
	}

	log.Trace().Msgf("parsing memory.limit_in_bytes content %s", memoryLimitsInBytesRaw)
	memoryLimitsInBytes, err := strconv.Atoi(strings.Fields(string(memoryLimitsInBytesRaw))[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse memory.limit_in_bytes: %s. skip adapting cpu load to container limits.", memoryLimitsInBytesRaw)
		return -1
	}

	if memoryLimitsInBytes == cgroupV1MemUnlimited {
		log.Debug().Msgf("container memory is unlimited (memory.limit_in_bytes=-1). skip adapting memory consumption to container limits.")
		return -1
	}

	return memoryLimitsInBytes
}

func readCGroupV2MemLimit(cGroupPath string, fs fileSystem) int {
	memoryMaxPath := filepath.Join("/sys/fs/cgroup", cGroupPath, "memory.max")
	memoryMaxRaw, err := fs.ReadFile(memoryMaxPath)
	if err != nil || len(memoryMaxRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read memory.max '%s. skip adapting memory consumption to container limits.", memoryMaxPath)
		return -1
	}

	log.Trace().Msgf("parsing memory.max content %s", memoryMaxRaw)
	memoryMax := strings.Fields(string(memoryMaxRaw))
	if memoryMax[0] == "max" {
		log.Debug().Msgf("container memory is unlimited (memory.max=max). skip adapting memory consumption to container limits.")
		return -1
	}

	memoryMaxInBytes, err := strconv.Atoi(memoryMax[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse memory.max: %s. skip adapting memory consumption to container limits.", memoryMaxRaw)
		return -1
	}

	return memoryMaxInBytes
}
