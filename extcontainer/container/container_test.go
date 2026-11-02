// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package container

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/stretchr/testify/assert"
)

func Test_autoDetect(t *testing.T) {
	noLinks := func(path string) (string, error) { return path, nil }
	tests := []struct {
		name    string
		sockets []string
		resolve func(string) (string, error)
		want    types.Runtime
	}{
		{"nothing", nil, noLinks, ""},
		{"docker", []string{types.DefaultSocketDocker}, noLinks, types.RuntimeDocker},
		{"docker wins over podman", []string{types.DefaultSocketDocker, types.DefaultSocketPodman}, noLinks, types.RuntimeDocker},
		{"podman", []string{types.DefaultSocketPodman}, noLinks, types.RuntimePodman},
		{"docker socket linked to podman (podman-docker)", []string{types.DefaultSocketDocker, types.DefaultSocketPodman}, func(path string) (string, error) {
			if path == types.DefaultSocketDocker {
				return types.DefaultSocketPodman, nil
			}
			return path, nil
		}, types.RuntimePodman},
		{"docker socket not resolvable", []string{types.DefaultSocketDocker}, func(string) (string, error) { return "", errors.New("boom") }, types.RuntimeDocker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := func(path string) bool {
				for _, s := range tt.sockets {
					if s == path {
						return true
					}
				}
				return false
			}
			assert.Equal(t, tt.want, autoDetect(exists, tt.resolve))
		})
	}
}

type fakeClient struct {
	types.Client
	runtime types.Runtime
	info    *types.OciRuntimeInfo
}

func (f fakeClient) Runtime() types.Runtime { return f.runtime }

type fakeProviderClient struct {
	fakeClient
	err error
}

func (f fakeProviderClient) OciRuntimeInfo(context.Context) (types.OciRuntimeInfo, error) {
	if f.err != nil {
		return types.OciRuntimeInfo{}, f.err
	}
	return *f.info, nil
}

func TestResolveOciRuntimeConfig(t *testing.T) {
	crun := &types.OciRuntimeInfo{Path: "/usr/bin/crun", Root: "/run/crun"}
	tests := []struct {
		name   string
		env    map[string]string
		cfg    ociruntime.Config
		client types.Client
		want   ociruntime.Config
	}{
		{
			name:   "docker keeps its defaults",
			cfg:    ociruntime.Config{Path: "runc"},
			client: fakeClient{runtime: types.RuntimeDocker},
			want:   ociruntime.Config{Path: "runc", Root: types.DefaultRuncRootDocker},
		},
		{
			name:   "podman uses the engine's runtime",
			cfg:    ociruntime.Config{Path: "runc"},
			client: fakeProviderClient{fakeClient: fakeClient{runtime: types.RuntimePodman, info: crun}},
			want:   ociruntime.Config{Path: "/usr/bin/crun", Root: "/run/crun"},
		},
		{
			name:   "explicit path and root win",
			env:    map[string]string{"STEADYBIT_EXTENSION_OCIRUNTIME_PATH": "runc"},
			cfg:    ociruntime.Config{Path: "runc", Root: "/custom"},
			client: fakeProviderClient{fakeClient: fakeClient{runtime: types.RuntimePodman, info: crun}},
			want:   ociruntime.Config{Path: "runc", Root: "/custom"},
		},
		{
			name:   "deprecated runc path variable counts as explicit",
			env:    map[string]string{"STEADYBIT_EXTENSION_RUNC_PATH": "runc"},
			cfg:    ociruntime.Config{Path: "runc"},
			client: fakeProviderClient{fakeClient: fakeClient{runtime: types.RuntimePodman, info: crun}},
			want:   ociruntime.Config{Path: "runc", Root: "/run/crun"},
		},
		{
			name:   "detection failure falls back to defaults",
			cfg:    ociruntime.Config{Path: "runc"},
			client: fakeProviderClient{fakeClient: fakeClient{runtime: types.RuntimePodman}, err: errors.New("down")},
			want:   ociruntime.Config{Path: "runc", Root: types.DefaultRuncRootPodman},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"STEADYBIT_EXTENSION_OCIRUNTIME_PATH", "STEADYBIT_EXTENSION_RUNC_PATH"} {
				if v, ok := tt.env[k]; ok {
					t.Setenv(k, v)
				} else if _, set := os.LookupEnv(k); set {
					t.Setenv(k, "")
					_ = os.Unsetenv(k)
				}
			}
			assert.Equal(t, tt.want, ResolveOciRuntimeConfig(context.Background(), tt.cfg, tt.client))
		})
	}
}
