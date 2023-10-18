// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package runc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/steadybit/extension-container/pkg/utils"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/trace"
	"strconv"
	"time"
)

type Runc interface {
	State(ctx context.Context, id string) (*ContainerState, error)
	Create(ctx context.Context, image, id string) (ContainerBundle, error)
	Run(ctx context.Context, container ContainerBundle, ioOpts IoOpts) error
	Delete(ctx context.Context, id string, force bool) error
}

type ContainerBundle interface {
	EditSpec(ctx context.Context, editors ...SpecEditor) error
	MountFromProcess(ctx context.Context, fromPid int, fromPath, mountpoint string) error
	CopyFileFromProcess(ctx context.Context, pid int, fromPath, toPath string) error
	Path() string
	ContainerId() string
	Remove() error
}

type defaultRunc struct {
	Root          string
	Debug         bool
	SystemdCgroup bool
	Rootless      string
}

type runcContainerBundle struct {
	id         string
	path       string
	finalizers []func() error
	runc       *defaultRunc
}

func (b *runcContainerBundle) Path() string {
	return b.path
}

func (b *runcContainerBundle) ContainerId() string {
	return b.id
}

func (b *runcContainerBundle) addFinalizer(f func() error) {
	b.finalizers = append(b.finalizers, f)
}

func (b *runcContainerBundle) Remove() error {
	var errs []error
	for i := len(b.finalizers) - 1; i >= 0; i-- {
		errs = append(errs, b.finalizers[i]())
	}
	return errors.Join(errs...)
}

type ContainerState struct {
	ID          string            `json:"id"`
	Pid         int               `json:"pid"`
	Status      string            `json:"status"`
	Bundle      string            `json:"bundle"`
	Rootfs      string            `json:"rootfs"`
	Created     time.Time         `json:"created"`
	Annotations map[string]string `json:"annotations"`
}

func NewRunc(runtime types.Runtime) Runc {
	root := config.Config.RuncRoot
	if root == "" {
		root = runtime.DefaultRuncRoot()
	}

	return &defaultRunc{
		SystemdCgroup: config.Config.RuncSystemdCgroup,
		Rootless:      config.Config.RuncRootless,
		Root:          root,
		Debug:         config.Config.RuncDebug,
	}
}

func (r *defaultRunc) State(ctx context.Context, id string) (*ContainerState, error) {
	defer trace.StartRegion(ctx, "runc.State").End()
	cmd := r.command(ctx, "state", id)
	var outputBuffer, errorBuffer bytes.Buffer
	cmd.Stdout = &outputBuffer
	cmd.Stderr = &errorBuffer
	err := cmd.Run()
	output := outputBuffer.Bytes()
	stderr := errorBuffer.Bytes()
	if err != nil {
		return nil, fmt.Errorf("ws (%s): %s", err, stderr, output)
	}

	log.Trace().Str("output", string(output)).Str("stderr", string(stderr)).Msg("get container state")

	var state ContainerState
	if err := unmarshalGuarded(output, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container state: %w", err)
	}
	return &state, nil
}

func (r *defaultRunc) Create(ctx context.Context, image string, id string) (ContainerBundle, error) {
	defer trace.StartRegion(ctx, "runc.Create").End()

	bundle := runcContainerBundle{
		id:   id,
		path: filepath.Join("/tmp/steadybit/containers", id),
		runc: r,
	}
	_ = os.RemoveAll(bundle.path)

	success := false
	defer func() {
		if !success {
			err := bundle.Remove()
			if err != nil {
				log.Warn().Err(err).Msg("failed to run bundle finalizers")
			}
		}
	}()

	log.Trace().Str("bundle", bundle.path).Msg("creating container bundle")
	if err := os.MkdirAll(bundle.path, 0775); err != nil {
		return nil, fmt.Errorf("failed to create directory '%s': %w", bundle.path, err)
	}
	bundle.addFinalizer(func() error {
		log.Trace().Str("bundle", bundle.path).Msg("removing container bundle")
		return os.RemoveAll(bundle.path)
	})

	var imagePath string
	if imageStat, err := os.Stat(image); err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	} else if imageStat.IsDir() {
		if abs, err := filepath.Abs(image); err == nil {
			imagePath = abs
		} else {
			log.Debug().Err(err).Str("image", image).Msg("failed to get absolute path for image")
			imagePath = image
		}
	} else {
		extractPath := filepath.Join("/tmp/", image, strconv.FormatInt(imageStat.ModTime().Unix(), 10))
		if err := extractImage(image, extractPath); err != nil {
			return nil, fmt.Errorf("failed to extract image: %w", err)
		}
		imagePath = extractPath
	}

	if err := bundle.mountRootfsOverlay(ctx, imagePath); err != nil {
		return nil, fmt.Errorf("failed to mount image: %w", err)
	}

	if err := r.createSpec(ctx, bundle.path); err != nil {
		return nil, fmt.Errorf("failed to create container spec: %w", err)
	}

	log.Trace().Str("bundle", bundle.path).Str("id", id).Msg("prepared container bundle")
	success = true
	return &bundle, nil
}

func (r *defaultRunc) Delete(ctx context.Context, id string, force bool) error {
	defer trace.StartRegion(ctx, "runc.Delete").End()
	log.Trace().Str("id", id).Msg("deleting container")
	if output, err := r.command(ctx, "delete", fmt.Sprintf("--force=%t", force), id).CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}
	return nil
}

type IoOpts struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func InheritStdIo() IoOpts {
	return IoOpts{
		Stdin:  nil,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

func (o IoOpts) WithStdin(reader io.Reader) IoOpts {
	return IoOpts{
		Stdin:  reader,
		Stdout: o.Stdout,
		Stderr: o.Stderr,
	}
}

func (r *defaultRunc) Run(ctx context.Context, container ContainerBundle, ioOpts IoOpts) error {
	defer trace.StartRegion(ctx, "runc.Run").End()
	bundle, ok := container.(*runcContainerBundle)
	if !ok {
		return fmt.Errorf("invalid bundle type: %T", container)
	}

	log.Trace().Str("id", bundle.id).Msg("running container")

	cmd := r.command(ctx, "run", "--bundle", bundle.path, bundle.id)
	cmd.Stdin = ioOpts.Stdin
	cmd.Stdout = ioOpts.Stdout
	cmd.Stderr = ioOpts.Stderr
	err := cmd.Run()

	log.Trace().Str("id", bundle.id).Int("exitCode", cmd.ProcessState.ExitCode()).Msg("container exited")
	return err
}

type SpecEditor func(spec *specs.Spec)

func (b *runcContainerBundle) EditSpec(ctx context.Context, editors ...SpecEditor) error {
	defer trace.StartRegion(ctx, "runc.EditSpec").End()
	spec, err := readSpec(filepath.Join(b.path, "config.json"))
	if err != nil {
		return err
	}

	withDefaults(spec)

	for _, fn := range editors {
		fn(spec)
	}
	err = writeSpec(filepath.Join(b.path, "config.json"), spec)
	log.Trace().Str("bundle", b.path).Interface("createSpec", spec).Msg("written runc createSpec")
	return err
}

func (r *defaultRunc) createSpec(ctx context.Context, bundle string) error {
	defer trace.StartRegion(ctx, "runc.Spec").End()
	log.Trace().Str("bundle", bundle).Msg("creating container createSpec")
	output, err := r.command(ctx, "spec", "--bundle", bundle).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
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

func (r *defaultRunc) command(ctx context.Context, args ...string) *exec.Cmd {
	return utils.RootCommandContext(ctx, "runc", append(r.args(), args...)...)
}

func (r *defaultRunc) args() []string {
	var out []string
	if r.Root != "" {
		out = append(out, "--root", r.Root)
	}
	if r.Debug {
		out = append(out, "--debug")
	}
	if r.SystemdCgroup {
		out = append(out, "--systemd-cgroup")
	}
	if r.Rootless != "" {
		out = append(out, "--rootless", r.Rootless)
	}
	return out
}

func (b *runcContainerBundle) mountRootfsOverlay(ctx context.Context, image string) error {
	upper := filepath.Join(b.path, "upper")
	err := os.MkdirAll(upper, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", upper, err)
	}

	work := filepath.Join(b.path, "work")
	err = os.MkdirAll(work, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", work, err)
	}

	rootfs := filepath.Join(b.path, "rootfs")
	err = os.MkdirAll(rootfs, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", rootfs, err)
	}

	log.Trace().
		Str("lowerdir", image).
		Str("upper", upper).
		Str("work", work).
		Str("rootfs", rootfs).
		Msg("mounting overlay")
	out, err := utils.RootCommandContext(ctx,
		"mount",
		"-t",
		"overlay",
		"-o",
		fmt.Sprintf("rw,relatime,lowerdir=%s,upperdir=%s,workdir=%s", image, upper, work),
		"overlay",
		rootfs).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	b.addFinalizer(func() error {
		return unmount(context.Background(), rootfs)
	})
	return nil
}

func extractImage(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		//image was already extracted.
		return nil
	}

	err := os.MkdirAll(dst, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", dst, err)
	}

	log.Trace().Str("src", src).Str("dst", dst).Msg("extracting sidecar image")
	out, err := exec.Command("tar", "-C", dst, "-xf", src).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func unmarshalGuarded(output []byte, v any) error {
	err := json.Unmarshal(output, v)
	if err == nil {
		return nil
	}

	if output[0] != '{' && bytes.Contains(output, []byte("{")) && bytes.Contains(output, []byte("}")) {
		if err := json.Unmarshal(output[bytes.IndexByte(output, '{'):], v); err == nil {
			return nil
		}
	}

	return err
}

func (b *runcContainerBundle) CopyFileFromProcess(ctx context.Context, pid int, fromPath, toPath string) error {
	defer trace.StartRegion(ctx, "utils.CopyFileFromProcessToBundle").End()
	var out bytes.Buffer
	cmd := utils.RootCommandContext(ctx, "cat", filepath.Join("/proc", strconv.Itoa(pid), "root", fromPath))
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, out.String())
	}

	return os.WriteFile(filepath.Join(b.path, "rootfs", toPath), out.Bytes(), 0644)
}

func (b *runcContainerBundle) MountFromProcess(ctx context.Context, fromPid int, fromPath, toPath string) error {
	defer trace.StartRegion(ctx, "utils.MountFromProcessToBundle").End()

	mountpoint := filepath.Join(b.path, "rootfs", toPath)
	log.Trace().
		Int("fromPid", fromPid).
		Str("fromPath", fromPath).
		Str("mountpoint", mountpoint).
		Msg("mount from process to bundle")

	if err := os.Mkdir(mountpoint, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("could not create mountpoint %s: %w", mountpoint, err)
	}

	var out bytes.Buffer
	cmd := utils.RootCommandContext(ctx, "nsmount", strconv.Itoa(fromPid), fromPath, strconv.Itoa(os.Getpid()), mountpoint)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, out.String())
	}
	b.addFinalizer(func() error {
		return unmount(context.Background(), mountpoint)
	})
	return nil
}

func unmount(ctx context.Context, path string) error {
	log.Trace().Str("path", path).Msg("unmounting")
	out, err := utils.RootCommandContext(ctx, "umount", "-v", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
