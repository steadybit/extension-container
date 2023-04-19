// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"context"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"testing"
	"time"
)

func TestWithMinikube(t *testing.T) {
	WithMinikube(t, []WithMinikubeTestCase{
		{
			Name: "target discovery",
			Test: testDiscovery,
		}, {
			Name: "stop container",
			Test: testStopContainer,
		}, {
			Name: "pause container",
			Test: testPauseContainer,
		}, {
			Name: "stress cpu",
			Test: testStressCpu,
		}, {
			Name: "stress memory",
			Test: testStressMemory,
		}, {
			Name: "stress io",
			Test: testStressIo,
		},
	})
}

func testStressCpu(t *testing.T, m *Minikube, e *Extension) {
	pod, err := createBusyBoxPod(m, "bb-stress-cpu")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	status, err := getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)

	target := action_kit_api.Target{
		Attributes: map[string][]string{
			"container.id": {status.ContainerID},
		},
	}
	config := struct {
		Duration int `json:"duration"`
		CpuLoad  int `json:"cpuLoad"`
		Workers  int `json:"workers"`
	}{Duration: 500, Workers: 0, CpuLoad: 50}
	exec := e.RunAction("com.github.steadybit.extension_container.container.stress-cpu", target, config)
	assertProcessRunningInContainer(t, m, "bb-stress-cpu", "busybox", "stress-ng")
	require.NoError(t, exec.Cancel())
}

func testStressMemory(t *testing.T, m *Minikube, e *Extension) {
	pod, err := createBusyBoxPod(m, "bb-stress-mem")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	status, err := getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)

	target := action_kit_api.Target{
		Attributes: map[string][]string{
			"container.id": {status.ContainerID},
		},
	}
	config := struct {
		Duration   int `json:"duration"`
		Percentage int `json:"percentage"`
	}{Duration: 5000, Percentage: 50}

	exec := e.RunAction("com.github.steadybit.extension_container.container.stress-mem", target, config)
	assertProcessRunningInContainer(t, m, "bb-stress-mem", "busybox", "stress-ng")
	require.NoError(t, exec.Cancel())
}

func testStressIo(t *testing.T, m *Minikube, e *Extension) {
	pod, err := createBusyBoxPod(m, "bb-stress-io")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	status, err := getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)

	target := action_kit_api.Target{
		Attributes: map[string][]string{
			"container.id": {status.ContainerID},
		},
	}
	config := struct {
		Duration   int    `json:"duration"`
		Path       string `json:"path"`
		Percentage int    `json:"percentage"`
		Workers    int    `json:"workers"`
	}{Duration: 5000, Workers: 1, Percentage: 50, Path: "/tmp"}
	exec := e.RunAction("com.github.steadybit.extension_container.container.stress-io", target, config)
	assertProcessRunningInContainer(t, m, "bb-stress-io", "busybox", "stress-ng")
	require.NoError(t, exec.Cancel())
}

func testPauseContainer(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("pause is not supported in cri-o")
	}

	pod, err := createBusyBoxPod(m, "bb-pause")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	status, err := getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)

	target := action_kit_api.Target{
		Attributes: map[string][]string{
			"container.id": {status.ContainerID},
		},
	}

	ts := make(chan time.Time, 10)
	go func() {
		require.NoError(t, waitForContainerStatusUsingContainerEngine(m, status.ContainerID, "paused"))
		ts <- time.Now()
		require.NoError(t, waitForContainerStatusUsingContainerEngine(m, status.ContainerID, "running"))
		ts <- time.Now()
	}()

	config := struct {
		Duration int `json:"duration"`
	}{Duration: 5000}
	err = e.RunAction("com.github.steadybit.extension_container.container.pause", target, config).Wait()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var start, end time.Time
	select {
	case <-ctx.Done():
		require.Failf(t, "timeout", "container was not paused")
	case start = <-ts:
	}
	select {
	case <-ctx.Done():
		require.Failf(t, "timeout", "container was not resumed")
	case end = <-ts:
	}
	duration := end.Sub(start)
	assert.True(t, duration > 4500*time.Millisecond && duration < 5500*time.Millisecond, "container was not paused for ~5s")
}
func testStopContainer(t *testing.T, m *Minikube, e *Extension) {
	pod, err := createBusyBoxPod(m, "bb-stop")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	status, err := getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)

	target := action_kit_api.Target{
		Attributes: map[string][]string{
			"container.id": {status.ContainerID},
		},
	}
	config := struct {
		Graceful bool `json:"graceful"`
	}{Graceful: true}
	err = e.RunAction("com.github.steadybit.extension_container.container.stop", target, config).Wait()
	require.NoError(t, err)

	require.NoError(t, waitForPodPhase(m, pod, corev1.PodFailed, 3000*time.Second))

	status, err = getContainerStatus(m, pod, "busybox")
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.NotNil(t, status.State.Terminated, "container should be terminated")
}
func testDiscovery(t *testing.T, m *Minikube, e *Extension) {
	pod, err := createBusyBoxPod(m, "bb-discovery")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = deletePod(m, pod) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	target, err := pollForTarget(ctx, e, func(target discovery_kit_api.Target) bool {
		return hasAttribute(target, "k8s.pod.name", "bb-discovery")
	})

	require.NoError(t, err)
	assert.Equal(t, target.TargetType, "com.github.steadybit.extension_container.container")
}
