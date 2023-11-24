// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package runc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/pkg/utils"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
)

func withDefaults(spec *specs.Spec) {
	spec.Root.Path = "rootfs"
	spec.Root.Readonly = true
	spec.Process.Terminal = false
	WithNamespace(specs.LinuxNamespace{Type: specs.MountNamespace})(spec)
}

func WithMountIfNotPresent(mount specs.Mount) SpecEditor {
	return func(spec *specs.Spec) {
		for _, m := range spec.Mounts {
			if m.Destination == mount.Destination {
				return
			}
		}
		spec.Mounts = append(spec.Mounts, mount)
	}
}

func WithHostname(hostname string) SpecEditor {
	return func(spec *specs.Spec) {
		WithNamespace(specs.LinuxNamespace{Type: specs.UTSNamespace})(spec)
		spec.Hostname = hostname
	}
}

func WithAnnotations(annotations map[string]string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Annotations = annotations
	}
}

func WithProcessArgs(args ...string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Process.Args = args
	}
}
func WithProcessCwd(cwd string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Process.Cwd = cwd
	}
}

func WithCapabilities(caps ...string) SpecEditor {
	return func(spec *specs.Spec) {
		for _, c := range caps {
			spec.Process.Capabilities.Bounding = appendIfMissing(spec.Process.Capabilities.Bounding, c)
			spec.Process.Capabilities.Effective = appendIfMissing(spec.Process.Capabilities.Effective, c)
			spec.Process.Capabilities.Inheritable = appendIfMissing(spec.Process.Capabilities.Inheritable, c)
			spec.Process.Capabilities.Permitted = appendIfMissing(spec.Process.Capabilities.Effective, c)
			spec.Process.Capabilities.Ambient = appendIfMissing(spec.Process.Capabilities.Ambient, c)
		}
	}
}

func appendIfMissing(list []string, str string) []string {
	for _, item := range list {
		if item == str {
			return list
		}
	}
	return append(list, str)
}

func WithCgroupPath(cgroupPath, child string) SpecEditor {
	return func(spec *specs.Spec) {
		spec.Linux.CgroupsPath = filepath.Join(cgroupPath, child)
	}
}

func WithNamespaces(ns []specs.LinuxNamespace) SpecEditor {
	return func(spec *specs.Spec) {
		for _, namespace := range ns {
			WithNamespace(namespace)(spec)
		}
	}
}

func WithNamespace(ns specs.LinuxNamespace) SpecEditor {
	return func(spec *specs.Spec) {
		for i, namespace := range spec.Linux.Namespaces {
			if namespace.Type == ns.Type {
				spec.Linux.Namespaces[i] = ns
				return
			}
		}
		spec.Linux.Namespaces = append(spec.Linux.Namespaces, ns)
	}
}

func CreateBundle(ctx context.Context, r Runc, config utils.TargetContainerConfig, containerId string, tempPath string, processArgs []string, cGroupChild string, mountpoint string) (ContainerBundle, error) {
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
		WithHostname(containerId),
		WithAnnotations(map[string]string{
			"com.steadybit.sidecar": "true",
		}),
		WithProcessArgs(processArgs...),
		WithProcessCwd("/tmp"),
		WithCgroupPath(config.CGroupPath, cGroupChild),
		WithNamespaces(utils.ToLinuxNamespaces(utils.FilterNamespaces(config.Namespaces, specs.PIDNamespace))),
		WithCapabilities("CAP_SYS_RESOURCE"),
		WithMountIfNotPresent(specs.Mount{
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

func RunBundle(runc Runc, bundle ContainerBundle, cond *sync.Cond, exited *bool, resultError *error, progname string) error {

	var outb bytes.Buffer
	pr, pw := io.Pipe()
	writer := io.MultiWriter(&outb, pw)

	cmd, err := runc.RunCommand(context.Background(), bundle)
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err != nil {
		return fmt.Errorf("failed to run %s: %w", progname, err)
	}

	go func() {
		defer func() { _ = pr.Close() }()
		bufReader := bufio.NewReader(pr)

		for {
			if line, err := bufReader.ReadString('\n'); err != nil {
				break
			} else {
				log.Debug().Str("id", bundle.ContainerId()).Msg(line)
			}
		}
	}()

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start %s: %w", progname, err)
	}

	go func() {
		defer func() { _ = pw.Close() }()
		err := cmd.Wait()
		log.Trace().Str("id", bundle.ContainerId()).Int("exitCode", cmd.ProcessState.ExitCode()).Msg(progname + " exited")

		cond.L.Lock()
		defer cond.L.Unlock()

		*exited = true
		var exitErr *exec.ExitError
		if errors.As(*resultError, &exitErr) {
			exitErr.Stderr = outb.Bytes()
			*resultError = exitErr
		} else {
			*resultError = err
		}

		cond.Broadcast()
	}()
	return nil
}
