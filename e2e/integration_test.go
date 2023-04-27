// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"context"
	"fmt"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-container/pkg/container/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"testing"
	"time"
)

func TestWithMinikube(t *testing.T) {
	WithMinikube(t, types.AllRuntimes, []WithMinikubeTestCase{
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
		}, {
			Name: "network blackhole",
			Test: testNetworkBlackhole,
		}, {
			Name: "network delay",
			Test: testNetworkDelay,
		}, {
			Name: "network block dns",
			Test: testNetworkBlockDns,
		}, {
			Name: "network limit bandwidth",
			Test: testNetworkLimitBandwidth,
		}, {
			Name: "network package loss",
			Test: testNetworkPackageLoss,
		}, {
			Name: "network package corruption",
			Test: testNetworkPackageCorruption,
		},
	})
}

func testNetworkDelay(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	netperf := netperf{minikube: m}
	err := netperf.Deploy("delay")
	defer func() { _ = netperf.Delete() }()
	require.NoError(t, err)

	target, err := netperf.Target()
	require.NoError(t, err)

	tests := []struct {
		name        string
		ip          []string
		hostname    []string
		port        []string
		interfaces  []string
		WantedDelay bool
	}{
		{
			name:        "should delay all traffic",
			WantedDelay: true,
		},
		{
			name:        "should delay only port 5000 traffic",
			port:        []string{"5000"},
			interfaces:  []string{"eth0"},
			WantedDelay: true,
		},
		{
			name:        "should delay only port 80 traffic",
			port:        []string{"80"},
			WantedDelay: false,
		},
	}

	unaffectedLatency, err := netperf.MeasureLatency()
	require.NoError(t, err)

	for _, tt := range tests {
		config := struct {
			Duration     int      `json:"duration"`
			Delay        int      `json:"networkDelay"`
			Jitter       bool     `json:"networkDelayJitter"`
			Ip           []string `json:"ip"`
			Hostname     []string `json:"hostname"`
			Port         []string `json:"port"`
			NetInterface []string `json:"networkInterface"`
		}{
			Duration:     10000,
			Delay:        200,
			Jitter:       false,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction("container.network_delay", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			latency, err := netperf.MeasureLatency()
			require.NoError(t, err)
			delay := latency - unaffectedLatency
			if tt.WantedDelay {
				require.True(t, delay > 200*time.Millisecond, "service should be delayed >200ms but was delayed %s", delay.String())
			} else {
				require.True(t, delay < 50*time.Millisecond, "service should not be delayed but was delayed %s", delay.String())
			}
			require.NoError(t, action.Cancel())

			latency, err = netperf.MeasureLatency()
			require.NoError(t, err)
			delay = latency - unaffectedLatency
			require.True(t, delay < 50*time.Millisecond, "service should not be delayed but was delayed %s", delay.String())
		})
	}
}

func testNetworkPackageLoss(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := iperf{minikube: m}
	err := iperf.Deploy("loss")
	defer func() { _ = iperf.Delete() }()
	require.NoError(t, err)

	target, err := iperf.Target()
	require.NoError(t, err)

	tests := []struct {
		name       string
		ip         []string
		hostname   []string
		port       []string
		interfaces []string
		WantedLoss bool
	}{
		{
			name:       "should loose packages on all traffic",
			WantedLoss: true,
		},
		{
			name:       "should loose packages only on port 5001 traffic",
			port:       []string{"5001"},
			interfaces: []string{"eth0"},
			WantedLoss: true,
		},
		{
			name:       "should loose packages only on port 80 traffic",
			port:       []string{"80"},
			WantedLoss: false,
		},
	}

	for _, tt := range tests {
		config := struct {
			Duration     int      `json:"duration"`
			Loss         int      `json:"networkLoss"`
			Ip           []string `json:"ip"`
			Hostname     []string `json:"hostname"`
			Port         []string `json:"port"`
			NetInterface []string `json:"networkInterface"`
		}{
			Duration:     10000,
			Loss:         10,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction("container.network_package_loss", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			loss, err := iperf.MeasurePackageLoss()
			require.NoError(t, err)
			if tt.WantedLoss {
				require.True(t, loss >= 7.0, "~10%% packages should be lost but was %.2f", loss)
			} else {
				require.True(t, loss <= 2.0, "packages should be lost but was %.2f", loss)
			}
			require.NoError(t, action.Cancel())

			loss, err = iperf.MeasurePackageLoss()
			require.NoError(t, err)
			require.True(t, loss <= 2.0, "packages should be lost but was %.2f", loss)
		})
	}
}

func testNetworkPackageCorruption(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := iperf{minikube: m}
	err := iperf.Deploy("corruption")
	defer func() { _ = iperf.Delete() }()
	require.NoError(t, err)

	target, err := iperf.Target()
	require.NoError(t, err)

	tests := []struct {
		name             string
		ip               []string
		hostname         []string
		port             []string
		interfaces       []string
		WantedCorruption bool
	}{
		{
			name:             "should corrupt packages on all traffic",
			WantedCorruption: true,
		},
		{
			name:             "should corrupt packages only on port 5001 traffic",
			port:             []string{"5001"},
			interfaces:       []string{"eth0"},
			WantedCorruption: true,
		},
		{
			name:             "should corrupt packages only on port 80 traffic",
			port:             []string{"80"},
			WantedCorruption: false,
		},
	}

	for _, tt := range tests {
		config := struct {
			Duration     int      `json:"duration"`
			Corruption   int      `json:"networkCorruption"`
			Ip           []string `json:"ip"`
			Hostname     []string `json:"hostname"`
			Port         []string `json:"port"`
			NetInterface []string `json:"networkInterface"`
		}{
			Duration:     10000,
			Corruption:   10,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction("container.network_package_corruption", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			loss, err := iperf.MeasurePackageLoss()
			require.NoError(t, err)
			if tt.WantedCorruption {
				require.True(t, loss >= 7.0, "~10%% packages should be corrupted but was %.2f", loss)
			} else {
				require.True(t, loss <= 2.0, "packages should be corrupted but was %.2f", loss)
			}
			require.NoError(t, action.Cancel())

			loss, err = iperf.MeasurePackageLoss()
			require.NoError(t, err)
			require.True(t, loss <= 2.0, "packages should be corrupted but was %.2f", loss)
		})
	}
}

func testNetworkLimitBandwidth(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := iperf{minikube: m}
	err := iperf.Deploy("bandwidth")
	defer func() { _ = iperf.Delete() }()
	require.NoError(t, err)

	target, err := iperf.Target()
	require.NoError(t, err)

	tests := []struct {
		name        string
		ip          []string
		hostname    []string
		port        []string
		interfaces  []string
		WantedLimit bool
	}{
		{
			name:        "should limit bandwidth on all traffic",
			WantedLimit: true,
		},
		{
			name:        "should limit bandwidth only on port 5001 traffic",
			port:        []string{"5001"},
			interfaces:  []string{"eth0"},
			WantedLimit: true,
		},
		{
			name:        "should limit bandwidth only on port 80 traffic",
			port:        []string{"80"},
			WantedLimit: false,
		},
	}

	unlimited, err := iperf.MeasureBandwidth()
	require.NoError(t, err)
	limit := unlimited / 3

	for _, tt := range tests {
		config := struct {
			Duration     int      `json:"duration"`
			Bandwidth    string   `json:"bandwidth"`
			Ip           []string `json:"ip"`
			Hostname     []string `json:"hostname"`
			Port         []string `json:"port"`
			NetInterface []string `json:"networkInterface"`
		}{
			Duration:     10000,
			Bandwidth:    fmt.Sprintf("%dmbit", int(limit)),
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction("container.network_bandwidth", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			bandwidth, err := iperf.MeasureBandwidth()
			require.NoError(t, err)
			if tt.WantedLimit {
				require.True(t, bandwidth <= (limit*1.05), "bandwidth should be ~%.2fmbit but was %.2fmbit", limit, bandwidth)
			} else {
				require.True(t, bandwidth > (unlimited*0.95), "bandwidth should not be limited (~%.2fmbit) but was %.2fmbit", unlimited, bandwidth)
			}
			require.NoError(t, action.Cancel())

			bandwidth, err = iperf.MeasureBandwidth()
			require.NoError(t, err)
			require.True(t, bandwidth > (unlimited*0.95), "bandwidth should not be limited (~%.2fmbit) but was %.2fmbit", unlimited, bandwidth)
		})
	}
}

func testNetworkBlackhole(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-network-blackhole")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	tests := []struct {
		name             string
		ip               []string
		hostname         []string
		port             []string
		WantedReachable  bool
		WantedReachesUrl bool
	}{
		{
			name:             "should blackhole all traffic",
			WantedReachable:  false,
			WantedReachesUrl: false,
		},
		{
			name:             "should blackhole only port 8080 traffic",
			port:             []string{"8080"},
			WantedReachable:  true,
			WantedReachesUrl: true,
		},
		{
			name:             "should blackhole only port 80, 443 traffic",
			port:             []string{"80", "443"},
			WantedReachable:  false,
			WantedReachesUrl: false,
		},
	}

	for _, tt := range tests {
		config := struct {
			Duration int      `json:"duration"`
			Ip       []string `json:"ip"`
			Hostname []string `json:"hostname"`
			Port     []string `json:"port"`
		}{
			Duration: 10000,
			Ip:       tt.ip,
			Hostname: tt.hostname,
			Port:     tt.port,
		}

		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, nginx.IsReachable(), "service should be reachable before blackhole")
			require.NoError(t, nginx.CanReach("https://google.com"), "service should reach url before blackhole")

			action, err := e.RunAction("container.network_blackhole", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.WantedReachable {
				require.NoError(t, nginx.IsReachable(), "service should be reachable during blackhole")
			} else {
				require.Error(t, nginx.IsReachable(), "service should not be reachable during blackhole")
			}

			if tt.WantedReachesUrl {
				require.NoError(t, nginx.CanReach("https://google.com"), "service should be reachable during blackhole")
			} else {
				require.Error(t, nginx.CanReach("https://google.com"), "service should not be reachable during blackhole")
			}

			require.NoError(t, action.Cancel())
			require.NoError(t, nginx.IsReachable(), "service should be reachable after blackhole")
			require.NoError(t, nginx.CanReach("https://google.com"), "service should reach url after blackhole")
		})
	}
}

func testNetworkBlockDns(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-network-block-dns")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	tests := []struct {
		name             string
		ip               []string
		hostname         []string
		dnsPort          uint
		WantedReachable  bool
		WantedReachesUrl bool
	}{
		{
			name:             "should block dns traffic",
			dnsPort:          53,
			WantedReachable:  true,
			WantedReachesUrl: false,
		},
		{
			name:             "should block dns traffic on port 5353",
			dnsPort:          5353,
			WantedReachable:  true,
			WantedReachesUrl: true,
		},
	}

	for _, tt := range tests {
		config := struct {
			Duration int  `json:"duration"`
			DnsPort  uint `json:"dnsPort"`
		}{
			Duration: 10000,
			DnsPort:  tt.dnsPort,
		}

		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, nginx.IsReachable(), "service should be reachable before block dns")
			require.NoError(t, nginx.CanReach("https://google.com"), "service should reach url before block dns")

			action, err := e.RunAction("container.network_block_dns", *target, config)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.WantedReachable {
				require.NoError(t, nginx.IsReachable(), "service should be reachable during block dns")
			} else {
				require.Error(t, nginx.IsReachable(), "service should not be reachable during block dns")
			}

			if tt.WantedReachesUrl {
				require.NoError(t, nginx.CanReach("https://google.com"), "service should be reachable during block dns")
			} else {
				require.ErrorContains(t, nginx.CanReach("https://google.com"), "Resolving timed out", "service should not be reachable during block dns")
			}

			require.NoError(t, action.Cancel())
			require.NoError(t, nginx.IsReachable(), "service should be reachable after block dns")
			require.NoError(t, nginx.CanReach("https://google.com"), "service should reach url after block dns")
		})
	}
}

func testStressCpu(t *testing.T, m *Minikube, e *Extension) {
	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-stress-cpu")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	config := struct {
		Duration int `json:"duration"`
		CpuLoad  int `json:"cpuLoad"`
		Workers  int `json:"workers"`
	}{Duration: 5000, Workers: 0, CpuLoad: 50}

	action, err := e.RunAction("container.stress_cpu", *target, config)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	assertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")
	require.NoError(t, action.Cancel())
}

func testStressMemory(t *testing.T, m *Minikube, e *Extension) {
	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-stress-mem")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	config := struct {
		Duration   int `json:"duration"`
		Percentage int `json:"percentage"`
	}{Duration: 5000, Percentage: 50}

	action, err := e.RunAction("container.stress_mem", *target, config)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	assertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")
	require.NoError(t, action.Cancel())
}

func testStressIo(t *testing.T, m *Minikube, e *Extension) {
	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-stress-io")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	config := struct {
		Duration   int    `json:"duration"`
		Path       string `json:"path"`
		Percentage int    `json:"percentage"`
		Workers    int    `json:"workers"`
	}{Duration: 5000, Workers: 1, Percentage: 50, Path: "/tmp"}
	action, err := e.RunAction("container.stress_io", *target, config)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	assertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")
	require.NoError(t, action.Cancel())
}

func testPauseContainer(t *testing.T, m *Minikube, e *Extension) {
	if m.runtime == "cri-o" {
		t.Skip("pause is not supported in cri-o")
	}

	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-pause")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	status, err := nginx.ContainerStatus()
	require.NoError(t, err)
	require.NotNil(t, status)

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
	action, err := e.RunAction("container.pause", *target, config)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	err = action.Wait()
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
	assert.True(t, duration >= 4*time.Second && duration < 5500*time.Millisecond, "container expected to be paused for ~5s but was paused for %s", duration)
}
func testStopContainer(t *testing.T, m *Minikube, e *Extension) {
	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-stop")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	config := struct {
		Graceful bool `json:"graceful"`
	}{Graceful: true}
	action, err := e.RunAction("container.stop", *target, config)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	require.NoError(t, action.Wait())

	require.NoError(t, m.WaitForPodPhase(nginx.Pod, corev1.PodSucceeded, 30*time.Second))

	status, err := nginx.ContainerStatus()
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.NotNil(t, status.State.Terminated, "container should be terminated")
}

func testDiscovery(t *testing.T, m *Minikube, e *Extension) {
	nginx := Nginx{minikube: m}
	err := nginx.Deploy("nginx-discovery")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	target, err := pollForTarget(ctx, e, func(target discovery_kit_api.Target) bool {
		return hasAttribute(target, "k8s.pod.name", "nginx-discovery")
	})

	require.NoError(t, err)
	assert.Equal(t, target.TargetType, "container")
}
