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
	cmd := RootCommandContext(ctx, "cat", filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, out.String())
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

	var sout bytes.Buffer
	var serr bytes.Buffer
	cmd := RootCommandContext(ctx, "lsns", "--task", strconv.Itoa(pid), "--output=ns,type,path", "--noheadings")
	cmd.Stdout = &sout
	cmd.Stderr = &serr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lsns --task %d %w: %s", pid, err, serr.String())
	}

	var namespaces []LinuxNamespaceWithInode
	for _, line := range strings.Split(strings.TrimSpace(sout.String()), "\n") {
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
	for i := range namespaces {
		refreshNamespacesUsingInode(ctx, &namespaces[i])
	}
}

var listNamespaceUsingInode = listNamespaceUsingInodeImpl

func refreshNamespacesUsingInode(ctx context.Context, ns *LinuxNamespaceWithInode) {
	defer trace.StartRegion(ctx, "utils.refreshNamespacesUsingInode").End()

	if ns == nil || ns.Inode == 0 {
		return
	}

	if _, err := os.Lstat(ns.Path); err == nil {
		return
	}

	log.Trace().Str("type", string(ns.Type)).
		Str("path", ns.Path).
		Uint64("inode", ns.Inode).
		Msg("refreshing namespace")

	out, err := listNamespaceUsingInode(ctx, ns.Inode)

	if err != nil {
		log.Warn().Str("type", string(ns.Type)).
			Err(err).
			Str("path", ns.Path).
			Uint64("inode", ns.Inode).
			Msg("failed refreshing namespace")
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			continue
		}

		ns.Path = fields[0]
		log.Trace().Str("type", string(ns.Type)).
			Str("path", ns.Path).
			Uint64("inode", ns.Inode).
			Msg("refreshed namespace")
		return
	}
}

func listNamespaceUsingInodeImpl(ctx context.Context, inode uint64) (string, error) {
	var sout, serr bytes.Buffer
	cmd := RootCommandContext(ctx, "lsns", strconv.FormatUint(inode, 10), "--output=path", "--noheadings")
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	if err := cmd.Run(); err != nil {
		return sout.String(), fmt.Errorf("lsns %w: %s", err, serr.String())
	}
	return sout.String(), nil
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

		refreshNamespacesUsingInode(ctx, &ns)

		if _, err := os.Lstat(ns.Path); err != nil && os.IsNotExist(err) {
			return fmt.Errorf("namespace %s doesn't exist", ns.Path)
		}
	}

	return nil
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
