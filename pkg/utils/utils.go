/*
 * Copyright 2023 steadybit GmbH. All rights reserved.
 */

package utils

import (
	"bytes"
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func ReadCgroupPath(pid int) (string, error) {
	var out bytes.Buffer
	cmd := RootCommandContext(context.Background(), "nsenter", "-t", "1", "-C", "--", "cat", filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, out.String())
	}

	minHid := 9999
	cgroup := ""
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 3 {
			continue
		}
		hid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if hid < minHid {
			minHid = hid
			cgroup = fields[2]
		}
	}
	if cgroup == "" {
		return "", fmt.Errorf("could not read cgroup for pid %d\n%s", pid, out.String())
	}
	return cgroup, nil
}

func ReadNamespaces(pid int) ([]specs.LinuxNamespace, error) {
	var out bytes.Buffer
	cmd := RootCommandContext(context.Background(), "nsenter", "-t", "1", "-C", "--", "lsns", "--task", strconv.Itoa(pid), "--output=type,path", "--noheadings")
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %s", err, out.String())
	}

	var namespaces []specs.LinuxNamespace
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ns := specs.LinuxNamespace{
			Type: toRuncNamespaceType(fields[0]),
			Path: fields[1],
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

// IsUsingHostNetwork determines weather the given process has the same network as the init process.
func IsUsingHostNetwork(pid int) (bool, error) {
	ns, err := ReadNamespaces(pid)
	if err != nil {
		return false, err
	}
	for _, n := range ns {
		if n.Type == specs.NetworkNamespace {
			return n.Path == "/proc/1/ns/net", nil
		}
	}
	return true, nil
}

func toRuncNamespaceType(t string) specs.LinuxNamespaceType {
	switch t {
	case "net":
		return specs.NetworkNamespace
	case "mnt":
		return specs.MountNamespace
	default:
		return specs.LinuxNamespaceType(t)
	}
}

func CopyFileFromProcessToBundle(bundle string, pid int, path string) error {
	var out bytes.Buffer
	cmd := RootCommandContext(context.Background(), "nsenter", "-t", "1", "-C", "--", "cat", filepath.Join("/proc", strconv.Itoa(pid), "root", path))
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", err, out.String())
	}

	return os.WriteFile(filepath.Join(bundle, "rootfs", path), out.Bytes(), 0644)
}

func RootCommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: 0,
			Gid: 0,
		},
	}
	return cmd
}
