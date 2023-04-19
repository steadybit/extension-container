// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package extcontainer

import (
	"github.com/steadybit/extension-container/pkg/container/types"
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
