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

func ReadNamespaces(pid int) ([]LinuxNamespaceWithInode, error) {
	var out bytes.Buffer
	cmd := RootCommandContext(context.Background(), "nsenter", "-t", "1", "-C", "--", "lsns", "--task", strconv.Itoa(pid), "--output=ns,type,path", "--noheadings")
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lsns %s: %s", err, out.String())
	}

	var namespaces []LinuxNamespaceWithInode
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
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

func ResolveNamespacesUsingInode(namespaces []LinuxNamespaceWithInode) []specs.LinuxNamespace {
	var runcNamespaces []specs.LinuxNamespace
	for _, ns := range namespaces {
		r := resolveNamespaceUsingInode(ns)
		runcNamespaces = append(runcNamespaces, r)
	}
	return runcNamespaces
}

func resolveNamespaceUsingInode(ns LinuxNamespaceWithInode) specs.LinuxNamespace {
	if ns.Inode == 0 {
		return ns.LinuxNamespace
	}

	var out bytes.Buffer
	cmd := RootCommandContext(context.Background(), "nsenter", "-t", "1", "-C", "--", "lsns", strconv.FormatUint(ns.Inode, 10), "--output=path", "--noheadings")
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		log.Debug().Err(err).Str("stderr", out.String()).Msgf("could not resolve %s namespace using inode %d to path. Falling back to possibly outdated path %s", ns.Type, ns.Inode, ns.Path)
		return ns.LinuxNamespace
	}

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 1 {
			continue
		}

		return specs.LinuxNamespace{
			Type: ns.Type,
			Path: fields[0],
		}
	}

	return ns.LinuxNamespace
}

func CheckNamespacesExists(namespaces []LinuxNamespaceWithInode, wantedTypes ...specs.LinuxNamespaceType) error {
	for _, ns := range namespaces {
		wanted := false
		if len(wantedTypes) == 0 {
			wanted = true
		} else {
			for _, wantedType := range wantedTypes {
				if ns.Type == wantedType {
					wanted = true
					break
				}
			}
		}

		if !wanted || ns.Path == "" {
			continue
		}

		resolved := resolveNamespaceUsingInode(ns)

		if _, err := os.Stat(resolved.Path); err != nil && os.IsNotExist(err) {
			return fmt.Errorf("namespace %s doesn't exist", resolved.Path)
		}
	}

	return nil
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

func CopyFileFromProcessToBundle(bundle string, pid int, path string) error {
	//TODO nsenter realy needed? replace with copy command
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
