// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	typecontainer "github.com/moby/moby/api/types/container"
	dclient "github.com/moby/moby/client"
	"github.com/steadybit/extension-container/extcontainer/container/types"
)

// Podman serves a Docker-compatible API, so the podman runtime reuses the Docker client with a few adjustments.
type podmanClient struct {
	*client
}

var _ types.OciRuntimeInfoProvider = (*podmanClient)(nil)

func NewPodman(address string) (types.Client, error) {
	dockerClient, err := newAPIClient(address)
	if err != nil {
		return nil, err
	}
	return &podmanClient{&client{
		docker:  dockerClient,
		runtime: types.RuntimePodman,
		// podman has no "restarting" state and rejects the whole list request when it is asked for it
		listStatuses:  []string{"running", "paused"},
		skipContainer: isPodmanInfraContainer,
	}}, nil
}

// isPodmanInfraContainer reports the infra container podman creates for every pod to hold its namespaces. It runs no
// workload and the compat API reports it without an image.
func isPodmanInfraContainer(c typecontainer.Summary) bool {
	if c.Image != "" {
		return false
	}
	for _, name := range c.Names {
		if strings.HasSuffix(name, "-infra") {
			return true
		}
	}
	return false
}

func (c *podmanClient) OciRuntimeInfo(ctx context.Context) (types.OciRuntimeInfo, error) {
	info, err := c.docker.Info(ctx, dclient.InfoOptions{})
	if err != nil {
		return types.OciRuntimeInfo{}, fmt.Errorf("failed to get podman info: %w", err)
	}

	name := info.Info.DefaultRuntime
	if name == "" {
		return types.OciRuntimeInfo{}, fmt.Errorf("podman did not report its default OCI runtime")
	}

	path := name
	if r, ok := info.Info.Runtimes[name]; ok && r.Path != "" {
		path = r.Path
	}

	return types.OciRuntimeInfo{Path: path, Root: podmanOciRuntimeRoot(c.Socket(), filepath.Base(path))}, nil
}

var rootlessSocket = regexp.MustCompile(`^(?:unix://)?(/run/user/\d+)/`)

// podmanOciRuntimeRoot returns the state directory podman's OCI runtime uses. Rootful podman leaves it at the runtime's
// default (/run/crun, /run/runc), rootless podman uses a directory below the user's runtime dir.
func podmanOciRuntimeRoot(socket, runtimeName string) string {
	if m := rootlessSocket.FindStringSubmatch(socket); m != nil {
		return filepath.Join(m[1], runtimeName)
	}
	return filepath.Join("/run", runtimeName)
}
