/*
 * Copyright 2023 steadybit GmbH. All rights reserved.
 */

package utils

import (
	"bytes"
	"context"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/trace"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var (
	sidecarImage     string
	sidecarImageOnce sync.Once
)

type LinuxNamespaceWithInode struct {
	specs.LinuxNamespace
	Inode uint64
}

func FilterNamespaces(ns []LinuxNamespaceWithInode, types ...specs.LinuxNamespaceType) []LinuxNamespaceWithInode {
	result := make([]LinuxNamespaceWithInode, 0, len(types))
	for _, n := range ns {
		for _, t := range types {
			if n.Type == t {
				result = append(result, n)
			}
		}
	}
	return result
}

func ToLinuxNamespaces(ns []LinuxNamespaceWithInode) []specs.LinuxNamespace {
	result := make([]specs.LinuxNamespace, 0, len(ns))
	for _, n := range ns {
		result = append(result, n.LinuxNamespace)
	}
	return result
}

func SidecarImagePath() string {
	sidecarImageOnce.Do(func() {
		if _, err := os.Stat("sidecar"); err == nil {
			sidecarImage = "sidecar"
			return
		}

		if _, err := os.Stat("sidecar.tar"); err == nil {
			sidecarImage = "sidecar.tar"
			return
		}

		if executable, err := os.Executable(); err == nil {
			executableDir := filepath.Dir(filepath.Clean(executable))

			candidate := filepath.Join(executableDir, "sidecar")
			if _, err := os.Stat(candidate); err == nil {
				sidecarImage = candidate
				return
			}

			candidate = filepath.Join(executableDir, "sidecar.tar")
			if _, err := os.Stat(candidate); err == nil {
				sidecarImage = candidate
				return
			}

			log.Fatal().Msg("Could not find sidecar image")
		}
	})
	return sidecarImage
}

func ReadCgroupPath(ctx context.Context, pid int) (string, error) {
	defer trace.StartRegion(ctx, "utils.ReadCgroupPath").End()
	var out bytes.Buffer
	cmd := RootCommandContext(ctx, "nsenter", "-t", "1", "-C", "--", "cat", filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
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

type TargetContainerConfig struct {
	ContainerID string                    `json:"id"`
	Pid         int                       `json:"pid"`
	Namespaces  []LinuxNamespaceWithInode `json:"namespaces"`
	CGroupPath  string                    `json:"cgroupPath"`
}

func ReadNamespaces(ctx context.Context, pid int) ([]LinuxNamespaceWithInode, error) {
	defer trace.StartRegion(ctx, "utils.ReadNamespaces").End()

	out, err := RootCommandContext(ctx, "nsenter", "-t", "1", "-C", "--", "lsns", "--task", strconv.Itoa(pid), "--output=ns,type,path", "--noheadings").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("lsns %w: %s", err, string(out))
	}

	var namespaces []LinuxNamespaceWithInode
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		inode, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			log.Warn().Err(err).Msgf("could not parse inode %s. omitting inode namespace information", fields[0])
		}
		ns := LinuxNamespaceWithInode{
			Inode: inode,
			LinuxNamespace: specs.LinuxNamespace{
				Type: toRuncNamespaceType(fields[1]),
				Path: fields[2],
			},
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
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

// RefreshNamespacesUsingInode if the denoted namespace path doesn't exist the path update it using updating also the list.
func RefreshNamespacesUsingInode(ctx context.Context, namespaces []LinuxNamespaceWithInode) {
	for _, ns := range namespaces {
		refreshNamespacesUsingInode(ctx, ns)
	}
}

func refreshNamespacesUsingInode(ctx context.Context, ns LinuxNamespaceWithInode) {
	defer trace.StartRegion(ctx, "utils.refreshNamespacesUsingInode").End()

	if ns.Inode == 0 {
		return
	}

	if _, err := os.Stat(ns.Path); err == nil {
		return
	}

	log.Trace().Str("path", ns.Path).Msgf("refreshing %s namespace using inode %d to path", ns.Type, ns.Inode)

	var out bytes.Buffer
	cmd := RootCommandContext(ctx, "nsenter", "-t", "1", "-C", "--", "lsns", strconv.FormatUint(ns.Inode, 10), "--output=path", "--noheadings")
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		log.Warn().Err(err).Str("stderr", out.String()).Msgf("could not refresh %s namespace using inode %d to path. %s doesn't exist anymore", ns.Type, ns.Inode, ns.Path)
		return
	}

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			continue
		}

		ns.Path = fields[0]
		return
	}
}

func CheckNamespacesExists(ctx context.Context, namespaces []LinuxNamespaceWithInode, wantedTypes ...specs.LinuxNamespaceType) error {
	defer trace.StartRegion(ctx, "utils.CheckNamespacesExists").End()

	filtered := namespaces
	if len(wantedTypes) > 0 {
		filtered = FilterNamespaces(namespaces, wantedTypes...)
	}

	for _, ns := range filtered {
		if ns.Path == "" {
			continue
		}

		refreshNamespacesUsingInode(ctx, ns)

		if _, err := os.Stat(ns.Path); err != nil && os.IsNotExist(err) {
			return fmt.Errorf("namespace %s doesn't exist", ns.Path)
		}
	}

	return nil
}

func CopyFileFromProcessToBundle(ctx context.Context, bundle string, pid int, path string) error {
	defer trace.StartRegion(ctx, "utils.CopyFileFromProcessToBundle").End()
	//TODO nsenter realy needed? replace with copy command
	var out bytes.Buffer
	cmd := RootCommandContext(ctx, "nsenter", "-t", "1", "-C", "--", "cat", filepath.Join("/proc", strconv.Itoa(pid), "root", path))
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
