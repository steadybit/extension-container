// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package runc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	syscall "syscall"
)

func EditSpec(bundleDir string, f func(spec *specs.Spec)) error {
	spec, err := readSpec(filepath.Join(bundleDir, "config.json"))
	if err != nil {
		return err
	}

	f(spec)

	return writeSpec(filepath.Join(bundleDir, "config.json"), spec)
}

func readSpec(file string) (*specs.Spec, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var spec specs.Spec

	if err := json.Unmarshal(content, &spec); err != nil {
		return nil, err
	}

	return &spec, nil
}

func writeSpec(file string, spec *specs.Spec) error {
	content, err := json.MarshalIndent(spec, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(file, content, 0644)
}

func AddMountIfNotPresent(spec *specs.Spec, mount specs.Mount) {
	for _, m := range spec.Mounts {
		if m.Destination == mount.Destination {
			return
		}
	}
	spec.Mounts = append(spec.Mounts, mount)
}

func AddCapabilities(spec *specs.Spec, caps ...string) {
	for _, c := range caps {
		spec.Process.Capabilities.Bounding = appendIfMissing(spec.Process.Capabilities.Bounding, c)
		spec.Process.Capabilities.Effective = appendIfMissing(spec.Process.Capabilities.Effective, c)
		spec.Process.Capabilities.Inheritable = appendIfMissing(spec.Process.Capabilities.Inheritable, c)
		spec.Process.Capabilities.Permitted = appendIfMissing(spec.Process.Capabilities.Effective, c)
		spec.Process.Capabilities.Ambient = appendIfMissing(spec.Process.Capabilities.Ambient, c)
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

func UseCgroupOf(spec *specs.Spec, pid int, child string) {
	cgroup, err := readCgroup(pid)
	if err != nil {
		log.Warn().Err(err).Int("pid", pid).Msg("Could not read cgroup")
		return
	}
	spec.Linux.CgroupsPath = filepath.Join(cgroup, child)
}

func UseNamespacesOf(spec *specs.Spec, pid int) {
	spec.Linux.Namespaces = []specs.LinuxNamespace{
		{
			Type: specs.PIDNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/pid", pid),
		},
		{
			Type: specs.IPCNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/ipc", pid),
		},
		{
			Type: specs.UTSNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/uts", pid),
		},
		{
			Type: specs.MountNamespace,
		},
		{
			Type: specs.CgroupNamespace,
			Path: fmt.Sprintf("/proc/%d/ns/cgroup", pid),
		},
	}
}

type MountInfo struct {
	MountID        string
	ParentID       string
	MajorMinor     string
	Root           string
	MountPoint     string
	MountOptions   string
	OptionalFields string
	FilesystemType string
	MountSource    string
	SuperOptions   string
}

func readCgroup(pid int) (string, error) {
	out, err := readProc("cgroup", pid)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "0::"), nil
}

func readProc(file string, pid int) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command("nsenter", "-t", "1", "-C", "--", "cat", filepath.Join("/proc", strconv.Itoa(pid), file))
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: 0,
			Gid: 0,
		},
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, out.String())
	}
	return out.String(), nil
}
