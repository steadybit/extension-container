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
	EditSpec(ctx context.Context, bundle string, editors ...SpecEditor) error
	Run(ctx context.Context, id, bundle string, ioOpts IoOpts) error
	Delete(ctx context.Context, id string, force bool) error
	PrepareBundle(ctx context.Context, image string, id string) (string, func() error, error)
}

type defaultRunc struct {
	Root          string
	Debug         bool
	SystemdCgroup bool
	Rootless      string
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
		return nil, fmt.Errorf("%s (%s): %s", err, stderr, output)
	}

	log.Trace().Str("output", string(output)).Str("stderr", string(stderr)).Msg("get container state")

	var state ContainerState
	if err := unmarshalGuarded(output, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal container state: %w", err)
	}
	return &state, nil
}

func (r *defaultRunc) spec(ctx context.Context, bundle string) error {
	defer trace.StartRegion(ctx, "runc.Spec").End()
	log.Trace().Str("bundle", bundle).Msg("creating container spec")
	output, err := r.command(ctx, "spec", "--bundle", bundle).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}
	return nil
}

type SpecEditor func(spec *specs.Spec)

func (r *defaultRunc) EditSpec(ctx context.Context, bundle string, editors ...SpecEditor) error {
	defer trace.StartRegion(ctx, "runc.EditSpec").End()
	spec, err := readSpec(filepath.Join(bundle, "config.json"))
	if err != nil {
		return err
	}

	withDefaults(spec)

	for _, fn := range editors {
		fn(spec)
	}
	err = writeSpec(filepath.Join(bundle, "config.json"), spec)
	log.Trace().Str("bundle", bundle).Interface("spec", spec).Msg("written runc spec")
	return err
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

func (r *defaultRunc) Run(ctx context.Context, id, bundle string, ioOpts IoOpts) error {
	defer trace.StartRegion(ctx, "runc.Run").End()
	log.Trace().Str("id", id).Msg("running container")

	cmd := r.command(ctx, "run", "--bundle", bundle, id)
	cmd.Stdin = ioOpts.Stdin
	cmd.Stdout = ioOpts.Stdout
	cmd.Stderr = ioOpts.Stderr
	err := cmd.Run()

	log.Trace().Str("id", id).Int("exitCode", cmd.ProcessState.ExitCode()).Msg("container exited")
	return err
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

func (r *defaultRunc) Delete(ctx context.Context, id string, force bool) error {
	defer trace.StartRegion(ctx, "runc.Delete").End()
	log.Trace().Str("id", id).Msg("deleting container")
	output, err := r.command(ctx, "delete", fmt.Sprintf("--force=%t", force), id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}
	log.Trace().Str("id", id).Msg("deleted container")
	return nil
}

func (r *defaultRunc) PrepareBundle(ctx context.Context, image string, id string) (string, func() error, error) {
	defer trace.StartRegion(ctx, "runc.PrepareBundle").End()
	bundle := filepath.Join("/tmp/steadybit/containers", id)

	_ = os.RemoveAll(bundle)

	log.Trace().Str("bundle", bundle).Msg("creating container bundle")
	if err := os.MkdirAll(bundle, 0775); err != nil {
		return "", nil, fmt.Errorf("failed to create directory '%s': %w", bundle, err)
	}

	removeBundle := func() error {
		log.Trace().Str("bundle", bundle).Msg("cleaning up container bundle")
		return os.RemoveAll(bundle)
	}

	var imagePath string
	if imageStat, err := os.Stat(image); err != nil {
		return bundle, removeBundle, fmt.Errorf("failed to read image: %w", err)
	} else if imageStat.IsDir() {
		if abs, err := filepath.Abs(image); err == nil {
			imagePath = abs
		} else {
			log.Debug().Err(err).Str("image", image).Msg("failed to get absolute path for image")
			imagePath = image
		}
	} else {
		extractPath := filepath.Join("/tmp/", image, strconv.FormatInt(imageStat.ModTime().Unix(), 10))
		if err := extractSidecarImage(image, extractPath); err != nil {
			return bundle, removeBundle, fmt.Errorf("failed to extract image: %w", err)
		}
		imagePath = extractPath
	}

	if err := mountOverlay(ctx, bundle, imagePath); err != nil {
		return bundle, removeBundle, fmt.Errorf("failed to mount image: %w", err)
	}

	cleanup := func() error {
		return errors.Join(unmountOverlay(ctx, bundle), removeBundle())
	}

	if err := r.spec(ctx, bundle); err != nil {
		return "", cleanup, err
	}

	log.Trace().Str("bundle", bundle).Str("id", id).Msg("prepared container bundle")
	return bundle, cleanup, nil
}

func mountOverlay(ctx context.Context, bundle string, image string) error {
	upper := filepath.Join(bundle, "upper")
	err := os.MkdirAll(upper, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", upper, err)
	}

	work := filepath.Join(bundle, "work")
	err = os.MkdirAll(work, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", work, err)
	}

	rootfs := filepath.Join(bundle, "rootfs")
	err = os.MkdirAll(rootfs, 0775)
	if err != nil {
		return fmt.Errorf("failed to create directory '%s': %w", rootfs, err)
	}

	log.Trace().Str("lowerdir", image).Str("upper", upper).Str("work", work).Str("rootfs", rootfs).Msg("mounting overlay")
	out, err := utils.RootCommandContext(ctx,
		"mount",
		"-t",
		"overlay",
		"-o",
		fmt.Sprintf("rw,relatime,lowerdir=%s,upperdir=%s,workdir=%s", image, upper, work),
		"overlay",
		rootfs).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

func extractSidecarImage(src, dst string) error {
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
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

func unmountOverlay(ctx context.Context, bundle string) error {
	rootfs := filepath.Join(bundle, "rootfs")
	log.Trace().Str("rootfs", rootfs).Msg("unmounting overlay")
	out, err := utils.RootCommandContext(ctx, "unmount", rootfs).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
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
