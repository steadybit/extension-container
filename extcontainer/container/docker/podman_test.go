// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEngine answers the few compat API calls the client makes, the way podman 5 does.
func fakeEngine(t *testing.T, rejectRestarting bool) (*httptest.Server, *[]string) {
	var listFilters []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.44")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/_ping":
			_, _ = w.Write([]byte("OK"))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			f := r.URL.Query().Get("filters")
			listFilters = append(listFilters, f)
			if rejectRestarting && strings.Contains(f, "restarting") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"cause":"invalid argument","message":"unknown container state: restarting: invalid argument","response":500}`))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "aaa", "Names": []string{"/nginx"}, "Image": "docker.io/library/nginx:alpine", "Labels": map[string]string{}},
				{"Id": "bbb", "Names": []string{"/01b6f4ba56f6-infra"}, "Image": "", "Labels": map[string]string{}},
			})
		case strings.HasSuffix(r.URL.Path, "/info"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"DefaultRuntime": "crun",
				"Runtimes": map[string]any{
					"crun": map[string]any{"path": "/usr/bin/crun"},
					"runc": map[string]any{"path": "/usr/bin/runc"},
				},
			})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &listFilters
}

func TestPodman_List_doesNotAskForRestartingAndSkipsInfraContainers(t *testing.T) {
	srv, filters := fakeEngine(t, true)
	c, err := NewPodman("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	containers, err := c.List(context.Background())

	require.NoError(t, err)
	require.Len(t, containers, 1)
	assert.Equal(t, "aaa", containers[0].Id())
	assert.NotContains(t, (*filters)[0], "restarting")
	assert.Equal(t, types.RuntimePodman, c.Runtime())
}

func TestDocker_List_keepsRestartingContainers(t *testing.T) {
	srv, filters := fakeEngine(t, false)
	c, err := New("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	containers, err := c.List(context.Background())

	require.NoError(t, err)
	assert.Len(t, containers, 2, "docker has no infra containers, nothing is skipped")
	assert.Contains(t, (*filters)[0], "restarting")
	assert.Equal(t, types.RuntimeDocker, c.Runtime())
}

func TestPodman_OciRuntimeInfo(t *testing.T) {
	srv, _ := fakeEngine(t, true)
	c, err := NewPodman("tcp://" + strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)

	info, err := c.(types.OciRuntimeInfoProvider).OciRuntimeInfo(context.Background())

	require.NoError(t, err)
	assert.Equal(t, types.OciRuntimeInfo{Path: "/usr/bin/crun", Root: "/run/crun"}, info)
}

func Test_podmanOciRuntimeRoot(t *testing.T) {
	tests := []struct {
		socket, runtime, want string
	}{
		{"unix:///run/podman/podman.sock", "crun", "/run/crun"},
		{"unix:///run/podman/podman.sock", "runc", "/run/runc"},
		{"/run/podman/podman.sock", "crun", "/run/crun"},
		{"unix:///run/user/1000/podman/podman.sock", "crun", "/run/user/1000/crun"},
		{"/run/user/1001/podman/podman.sock", "runc", "/run/user/1001/runc"},
	}
	for _, tt := range tests {
		t.Run(tt.socket+"/"+tt.runtime, func(t *testing.T) {
			assert.Equal(t, tt.want, podmanOciRuntimeRoot(tt.socket, tt.runtime))
		})
	}
}
