// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package runc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Runc struct {
	Root  string
	Debug bool
}

type Container struct {
	ID          string            `json:"id"`
	Pid         int               `json:"pid"`
	Status      string            `json:"status"`
	Bundle      string            `json:"bundle"`
	Rootfs      string            `json:"rootfs"`
	Created     time.Time         `json:"created"`
	Annotations map[string]string `json:"annotations"`
}

func (r *Runc) State(ctx context.Context, id string) (*Container, error) {
	output, err := r.command(ctx, "state", id).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, output)
	}
	var c Container
	if err := json.Unmarshal(output, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Runc) Spec(ctx context.Context, bundle string) error {
	output, err := r.command(ctx, "spec", "--bundle", bundle).CombinedOutput()
	if err != nil {
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

func (r *Runc) Run(ctx context.Context, id, bundle string, ioOpts IoOpts) error {
	cmd := r.command(ctx, "run", "--bundle", bundle, id)
	cmd.Stdin = ioOpts.Stdin
	cmd.Stdout = ioOpts.Stdout
	cmd.Stderr = ioOpts.Stderr
	return cmd.Run()
}

func (r *Runc) command(ctx context.Context, args ...string) *exec.Cmd {
	return rootCommandContext(ctx, "runc", append(r.args(), args...)...)
}

func (r *Runc) args() []string {
	out := []string{}
	if r.Root != "" {
		out = append(out, "--root", r.Root)
	}
	if r.Debug {
		out = append(out, "--debug")
	}
	return out
}

func (r *Runc) Delete(ctx context.Context, id string, force bool) error {
	output, err := r.command(ctx, "delete", fmt.Sprintf("--force=%t", force), id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, output)
	}
	return nil
}

func (r *Runc) PrepareBundle(ctx context.Context, id string) (string, error) {
	bundleDir := filepath.Join("/tmp/steadybit/containers", id)
	rootfs := filepath.Join(bundleDir, "rootfs")

	_ = os.RemoveAll(bundleDir)

	if err := os.MkdirAll(rootfs, 0775); err != nil {
		return "", fmt.Errorf("failed to create bundle dir: %w", err)
	}

	if out, err := exec.Command("tar", "-xf", "iproute2.tar", "-C", rootfs).CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to prepare rootfs dir: %s %w", out, err)
	}

	if err := r.Spec(ctx, bundleDir); err != nil {
		return "", err
	}

	return bundleDir, nil
}
