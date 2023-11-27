package diskfill

import (
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/container/runc"
	"github.com/steadybit/extension-container/pkg/utils"
	"strconv"
	"strings"
)

func CreateBundle(ctx context.Context, r runc.Runc, config utils.TargetContainerConfig, containerId string, tempPath string, processArgs func(tempPath string) []string, cGroupChild string, mountpoint string) (runc.ContainerBundle, error) {
	success := false
	bundle, err := r.Create(ctx, utils.SidecarImagePath(), containerId)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare bundle: %w", err)
	}
	defer func() {
		if !success {
			if err := bundle.Remove(); err != nil {
				log.Warn().Str("containerId", containerId).Err(err).Msg("failed to remove bundle")
			}
		}
	}()

	if tempPath != "" {
		if err := bundle.MountFromProcess(ctx, config.Pid, tempPath, mountpoint); err != nil {
			log.Warn().Err(err).Msgf("failed to mount %s", tempPath)
		} else {
			tempPath = mountpoint
		}
	}

	if err := bundle.EditSpec(ctx,
		runc.WithHostname(containerId),
		runc.WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		runc.WithProcessArgs(processArgs(tempPath)...),
		runc.WithProcessCwd("/tmp"),
		runc.WithCgroupPath(config.CGroupPath, cGroupChild),
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

	success = true

	return bundle, nil
}

type space struct {
	capacity  int
	used      int
	available int
}

func calculateSpace(lines []string) (space, error) {
	log.Debug().Msgf("calculateSpace: %v", lines)
	var keyValueMap = make(map[string]string)
	colNames := deleteEmpty(strings.Split(lines[0], " "))
	log.Debug().Msgf("colNames: %v", colNames)
	colValues := deleteEmpty(strings.Split(lines[1], " "))
	log.Debug().Msgf("colValues: %v", colValues)
	//remove empty string
	for idx, colValue := range colValues {
		keyValueMap[strings.ToLower(colNames[idx])] = colValue
	}
	log.Debug().Msgf("keyValueMap: %v", keyValueMap)
	capacity, err := strconv.Atoi(keyValueMap["1k-blocks"])
	if err != nil {
		return space{}, err
	}
	used, err := strconv.Atoi(keyValueMap["used"])
	if err != nil {
		return space{}, err
	}
	available, err := strconv.Atoi(keyValueMap["available"])
	if err != nil {
		return space{}, err
	}
	result := space{
		capacity:  capacity,
		used:      used,
		available: available,
	}
	return result, nil
}

func deleteEmpty(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}
