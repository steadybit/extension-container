// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/kataras/iris/v12/x/mathx"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/runc"
	"github.com/steadybit/action-kit/go/action_kit_commons/stress"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit"
	"github.com/steadybit/extension-kit/extutil"
	"golang.org/x/sync/syncmap"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/trace"
	"strconv"
	"strings"
)

type stressOptsProvider func(request action_kit_api.PrepareActionRequestBody) (stress.Opts, error)

type stressAction struct {
	runc         runc.Runc
	description  action_kit_api.ActionDescription
	optsProvider stressOptsProvider
	stresses     syncmap.Map
}

type StressActionState struct {
	Sidecar         stress.SidecarOpts
	ContainerID     string
	StressOpts      stress.Opts
	ExecutionId     uuid.UUID
	IgnoreExitCodes []int
}

// Make sure stressAction implements all required interfaces
var _ action_kit_sdk.Action[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStatus[StressActionState] = (*stressAction)(nil)
var _ action_kit_sdk.ActionWithStop[StressActionState] = (*stressAction)(nil)

func newStressAction(
	runc runc.Runc,
	description func() action_kit_api.ActionDescription,
	optsProvider stressOptsProvider,
) action_kit_sdk.Action[StressActionState] {
	return &stressAction{
		description:  description(),
		optsProvider: optsProvider,
		runc:         runc,
		stresses:     syncmap.Map{},
	}
}

func (a *stressAction) NewEmptyState() StressActionState {
	return StressActionState{}
}

func (a *stressAction) Describe() action_kit_api.ActionDescription {
	return a.description
}

func (a *stressAction) Prepare(ctx context.Context, state *StressActionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Prepare")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	containerId := request.Target.Attributes["container.id"]
	if len(containerId) == 0 {
		return nil, extension_kit.ToError("Target is missing the 'container.id' attribute.", nil)
	}
	state.ContainerID = containerId[0]

	processInfo, err := getProcessInfoForContainer(ctx, a.runc, RemovePrefix(state.ContainerID))
	if err != nil {
		return nil, extension_kit.ToError("Failed to prepare network settings.", err)
	}

	state.Sidecar = stress.SidecarOpts{
		TargetProcess: processInfo,
		ImagePath:     "/",
		IdSuffix:      RemovePrefix(state.ContainerID)[:8],
	}

	opts, err := a.optsProvider(request)
	if err != nil {
		return nil, err
	}

	readAndAdaptToCpuContainerLimits(ctx, processInfo.CGroupPath, &opts)

	state.StressOpts = opts
	state.ExecutionId = request.ExecutionId
	if !extutil.ToBool(request.Config["failOnOomKill"]) {
		state.IgnoreExitCodes = []int{137}
	}
	return nil, nil
}

func readAndAdaptToCpuContainerLimits(ctx context.Context, cGroupPath string, opts *stress.Opts) {
	if opts.CpuWorkers == nil {
		return
	}

	var cpuLimitInMilliCpu *float64
	if isCGroupV1() {
		cpuLimitInMilliCpu = readCGroupV1CpuLimit(cGroupPath)
	} else {
		cpuLimitInMilliCpu = readCGroupV2CpuLimit(cGroupPath)
	}

	if cpuLimitInMilliCpu != nil {
		adaptToCpuContainerLimits(*cpuLimitInMilliCpu, runtime.NumCPU(), opts)
	}
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

func readCGroupV1CpuLimit(cGroupPath string) *float64 {
	return readCGroupV1CpuLimitInternal(cGroupPath, osFileSystem{})
}

func readCGroupV1CpuLimitInternal(cGroupPath string, fs fileSystem) *float64 {
	cpuCfsQuotaPath := filepath.Join("/sys/fs/cgroup/cpu,cpuacct", cGroupPath, "cpu.cfs_quota_us")
	cpuCfsQuotaRaw, err := fs.ReadFile(cpuCfsQuotaPath)
	if err != nil || len(cpuCfsQuotaRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.cfs_quota_us '%s. skip adapting cpu load to container limits.", cpuCfsQuotaPath)
		return nil
	}
	log.Debug().Msgf("parsing cpu.cfs_quota_us content %s", cpuCfsQuotaRaw)
	cpuCfsQuota, err := strconv.Atoi(strings.Fields(string(cpuCfsQuotaRaw))[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpu.cfs_quota_us: %s. skip adapting cpu load to container limits.", cpuCfsQuotaRaw)
		return nil
	}

	if cpuCfsQuota == -1 {
		log.Info().Msgf("container cpu is unlimited. skip adapting cpu load to container limits. (cpu.cfs_quota_us=-1)")
		return nil
	}

	cpuCfsPeriodPath := filepath.Join("/sys/fs/cgroup/cpu,cpuacct", cGroupPath, "cpu.cfs_period_us")
	cpuCfsPeriodRaw, err := fs.ReadFile(cpuCfsPeriodPath)
	if err != nil || len(cpuCfsPeriodRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.cfs_period_us '%s. skip adapting cpu load to container limits.", cpuCfsPeriodPath)
		return nil
	}
	log.Debug().Msgf("parsing cpu.cfs_period_us content %s", cpuCfsPeriodRaw)
	cpuCfsPeriod, err := strconv.Atoi(strings.Fields(string(cpuCfsPeriodRaw))[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpu.cfs_period_us: %s. skip adapting cpu load to container limits.", cpuCfsPeriodRaw)
		return nil
	}

	cpuLimitInMilliCpu := float64(cpuCfsQuota) / float64(cpuCfsPeriod) * 1000
	log.Debug().Msgf("container cpu limit is %.0fm", cpuLimitInMilliCpu)
	return extutil.Ptr(cpuLimitInMilliCpu)
}

func readCGroupV2CpuLimit(cGroupPath string) *float64 {
	return readCGroupV2CpuLimitInternal(cGroupPath, osFileSystem{})
}

func readCGroupV2CpuLimitInternal(cGroupPath string, fs fileSystem) *float64 {
	cpuMaxCGroupPath := filepath.Join("/sys/fs/cgroup", cGroupPath, "cpu.max")
	cpuMaxCGroupRaw, err := fs.ReadFile(cpuMaxCGroupPath)
	if err != nil || len(cpuMaxCGroupRaw) == 0 {
		log.Warn().Err(err).Msgf("failed to read cpu.max '%s. skip adapting cpu load to container limits.", cpuMaxCGroupPath)
		return nil
	}

	log.Debug().Msgf("parsing cpu.max content %s", cpuMaxCGroupRaw)
	cpuMaxCGroup := strings.Fields(string(cpuMaxCGroupRaw))
	if len(cpuMaxCGroup) != 2 {
		log.Warn().Msgf("failed to parse cpu.max: %s. skip adapting cpu load to container limits.", cpuMaxCGroupRaw)
		return nil
	} else if cpuMaxCGroup[0] == "max" {
		log.Info().Msgf("container cpu is unlimited (cpu.max=max ....). skip adapting cpu load to container limits.")
		return nil
	}
	cpuLimitInMicroseconds, err := strconv.Atoi(cpuMaxCGroup[0])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpuLimitInMicroseconds: %s. skip adapting cpu load to container limits.", cpuMaxCGroup[0])
		return nil
	}
	cpuLimitPeriod, err := strconv.Atoi(cpuMaxCGroup[1])
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse cpuLimitPeriod: %s. skip adapting cpu load to container limits.", cpuMaxCGroup[1])
		return nil
	}
	cpuLimitInMilliCpu := float64(cpuLimitInMicroseconds) / float64(cpuLimitPeriod) * 1000
	log.Debug().Msgf("container cpu limit is %.0fm", cpuLimitInMilliCpu)
	return extutil.Ptr(cpuLimitInMilliCpu)
}

func adaptToCpuContainerLimits(cpuLimitInMilliCpu float64, cpuCount int, opts *stress.Opts) {
	desiredCpuConsumptionInMilliCpu := cpuLimitInMilliCpu * float64(opts.CpuLoad) / 100
	log.Debug().Msgf("desiredCpuConsumption is %.0fm - (%d%%)", desiredCpuConsumptionInMilliCpu, opts.CpuLoad)

	log.Debug().Msgf("cpu count is %d", cpuCount)
	if *opts.CpuWorkers == 0 {
		// user didn't specify the number of workers. we start as many workers as we need to reach the desired cpu consumption
		cpuWorkers := int(mathx.RoundUp(float64(desiredCpuConsumptionInMilliCpu)/1000, 0))
		desiredCpuConsumptionPerWorkerInMilliCpu := desiredCpuConsumptionInMilliCpu / float64(cpuWorkers)
		desiredCpuConsumptionPerWorkerInPercent := int(math.Round(desiredCpuConsumptionPerWorkerInMilliCpu / 10))
		log.Info().Msgf("container cpu limit is %.0fm. Starting %d workers with %d%% load.", cpuLimitInMilliCpu, cpuWorkers, desiredCpuConsumptionPerWorkerInPercent)
		opts.CpuWorkers = extutil.Ptr(cpuWorkers)
		opts.CpuLoad = desiredCpuConsumptionPerWorkerInPercent
	} else {
		// use the given number of workers
		desiredCpuConsumptionPerWorkerInMilliCpu := desiredCpuConsumptionInMilliCpu / float64(*opts.CpuWorkers)
		desiredCpuConsumptionPerWorkerInPercent := int(math.Round(desiredCpuConsumptionPerWorkerInMilliCpu / 10))
		log.Info().Msgf("container cpu limit is %.0fm. Starting %d workers with %d%% load.", cpuLimitInMilliCpu, *opts.CpuWorkers, desiredCpuConsumptionPerWorkerInPercent)
		opts.CpuLoad = desiredCpuConsumptionPerWorkerInPercent
	}
}

func (a *stressAction) Start(ctx context.Context, state *StressActionState) (*action_kit_api.StartResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Start")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	s, err := stress.New(ctx, a.runc, state.Sidecar, state.StressOpts)
	if err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	a.stresses.Store(state.ExecutionId, s)

	if err := s.Start(); err != nil {
		return nil, extension_kit.ToError("Failed to stress container", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Starting stress container %s with args %s", state.ContainerID, strings.Join(state.StressOpts.Args(), " ")),
			},
		}),
	}, nil
}

func (a *stressAction) Status(ctx context.Context, state *StressActionState) (*action_kit_api.StatusResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Status")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	exited, err := a.stressExited(state.ExecutionId)
	if !exited {
		return &action_kit_api.StatusResult{Completed: false}, nil
	}

	if err == nil {
		return &action_kit_api.StatusResult{
			Completed: true,
			Messages: &[]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Stessing container %s stopped", state.ContainerID),
				},
			},
		}, nil
	}

	errMessage := err.Error()

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		if len(exitErr.Stderr) > 0 {
			errMessage = fmt.Sprintf("%s\n%s", exitErr.Error(), string(exitErr.Stderr))
		}

		for _, ignore := range state.IgnoreExitCodes {
			if exitCode == ignore {
				return &action_kit_api.StatusResult{
					Completed: true,
					Messages: &[]action_kit_api.Message{
						{
							Level:   extutil.Ptr(action_kit_api.Warn),
							Message: fmt.Sprintf("stress-ng exited unexpectedly: %s", errMessage),
						},
					},
				}, nil
			}
		}
	}

	return &action_kit_api.StatusResult{
		Completed: true,
		Error: &action_kit_api.ActionKitError{
			Status: extutil.Ptr(action_kit_api.Failed),
			Title:  fmt.Sprintf("Failed to stress container: %s", errMessage),
		},
	}, nil
}

func (a *stressAction) Stop(ctx context.Context, state *StressActionState) (*action_kit_api.StopResult, error) {
	ctx, task := trace.NewTask(ctx, "action_stress.Stop")
	defer task.End()
	trace.Log(ctx, "actionId", a.description.Id)
	trace.Log(ctx, "executionId", state.ExecutionId.String())

	messages := make([]action_kit_api.Message, 0)

	stopped := a.stopStressContainer(state.ExecutionId)
	if stopped {
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Canceled stress container %s", state.ContainerID),
		})
	}

	return &action_kit_api.StopResult{
		Messages: &messages,
	}, nil
}

func (a *stressAction) stressExited(executionId uuid.UUID) (bool, error) {
	s, ok := a.stresses.Load(executionId)
	if !ok {
		return true, nil
	}
	return s.(*stress.Stress).Exited()
}

func (a *stressAction) stopStressContainer(executionId uuid.UUID) bool {
	s, ok := a.stresses.LoadAndDelete(executionId)
	if !ok {
		return false
	}
	s.(*stress.Stress).Stop()
	return true
}
