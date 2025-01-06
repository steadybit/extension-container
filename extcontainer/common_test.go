// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"context"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func Test_addPrefix(t *testing.T) {
	type args struct {
		containerId string
		runtime     types.Runtime
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"containerd", args{containerId: "test", runtime: types.RuntimeContainerd}, "containerd://test"},
		{"docker", args{containerId: "test", runtime: types.RuntimeDocker}, "docker://test"},
		{"cri-o", args{containerId: "test", runtime: types.RuntimeCrio}, "cri-o://test"},
		{"already has prefix", args{containerId: "docker://test", runtime: types.RuntimeDocker}, "docker://test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AddPrefix(tt.args.containerId, tt.args.runtime); got != tt.want {
				t.Errorf("addPrefix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_removePrefix(t *testing.T) {
	type args struct {
		containerId string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"containerd", args{containerId: "containerd://test"}, "test"},
		{"docker", args{containerId: "docker://test"}, "test"},
		{"cri-o", args{containerId: "cri-o://test"}, "test"},
		{"without prefix", args{containerId: "test"}, "test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemovePrefix(tt.args.containerId); got != tt.want {
				t.Errorf("removePrefix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getContainerTarget(t *testing.T) {
	tests := []struct {
		name                 string
		namespace            string
		disallowedNamespaces string
		wantErr              error
	}{
		{
			name:                 "should not return an error when no namespace are disallowed",
			namespace:            "kube-system",
			disallowedNamespaces: "",
		},
		{
			name:                 "should not return an error when namespace is disallowed, mismatch",
			namespace:            "test",
			disallowedNamespaces: "kube-system,gke-managed-*",
			wantErr:              nil,
		},
		{
			name:                 "should return an error when namespace is disallowed, direct match",
			namespace:            "kube-system",
			disallowedNamespaces: "kube-system,gke-managed-*",
			wantErr:              extension_kit.ToError("Container is in a namespace disallowed for attacks", nil),
		},
		{
			name:                 "should return an error when namespace is disallowed, direct glob match",
			namespace:            "gke-managed-cim-123",
			disallowedNamespaces: "kube-system,gke-gmp-system,composer-system,gke-managed-*",
			wantErr:              extension_kit.ToError("Container is in a namespace disallowed for attacks", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newMockedContainerClient()
			c.addContainer("test", map[string]string{"io.kubernetes.pod.namespace": tt.namespace})

			oldArgs := os.Args
			os.Args = []string{"extension"}
			defer func() { os.Args = oldArgs }()

			t.Setenv("STEADYBIT_EXTENSION_DISALLOW_K8S_NAMESPACES", tt.disallowedNamespaces)
			t.Setenv("STEADYBIT_EXTENSION_MEMFILL_PATH", "dummy")
			config.ParseConfiguration()

			_, _, err := getContainerTarget(context.Background(), c, action_kit_api.Target{
				Attributes: map[string][]string{"container.id": {"test"}},
			})

			assert.Equal(t, tt.wantErr, err)
		})
	}
}
