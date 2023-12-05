// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_test/client"
	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_test/validate"
	"github.com/steadybit/extension-container/extcontainer"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	executionContext = &action_kit_api.ExecutionContext{
		AgentAwsAccountId:   nil,
		RestrictedEndpoints: extutil.Ptr([]action_kit_api.RestrictedEndpoint{}),
	}
)

func TestWithMinikube(t *testing.T) {
	extFactory := e2e.HelmExtensionFactory{
		Name: "extension-container",
		Port: 8086,
		ExtraArgs: func(m *e2e.Minikube) []string {
			return []string{
				"--set", fmt.Sprintf("container.runtime=%s", m.Runtime),
				"--set", "logging.level=trace",
				"--set", "discovery.attributes.excludes={container.label.*}",
			}
		},
	}

	e2e.WithMinikube(t, getMinikubeOptions(), &extFactory, []e2e.WithMinikubeTestCase{
		{
			Name: "validate discovery",
			Test: validateDiscovery,
		},
		{
			Name: "target discovery",
			Test: testDiscovery,
		},
		{
			Name: "stop container",
			Test: testStopContainer,
		},
		{
			Name: "pause container",
			Test: testPauseContainer,
		},
		{
			Name: "stress cpu",
			Test: testStressCpu,
		},
		{
			Name: "stress memory",
			Test: testStressMemory,
		},
		{
			Name: "stress io",
			Test: testStressIo,
		},
		{
			Name: "network blackhole",
			Test: testNetworkBlackhole,
		},
		{
			Name: "network blackhole (3 containers in one pod)",
			Test: testNetworkBlackhole3Containers,
		},
		{
			Name: "network delay",
			Test: testNetworkDelay,
		},
		{
			Name: "network block dns",
			Test: testNetworkBlockDns,
		},
		{
			Name: "network limit bandwidth",
			Test: testNetworkLimitBandwidth,
		},
		{
			Name: "network package loss",
			Test: testNetworkPackageLoss,
		},
		{
			Name: "network package corruption",
			Test: testNetworkPackageCorruption,
		},
		{
			Name: "host network detection",
			Test: testHostNetwork,
		},
		{
			Name: "network delay two containers on the same network",
			Test: testNetworkDelayOnTwoContainers,
		},
		{
			Name: "fill disk",
			Test: testFillDisk,
		},
	})
}

func getMinikubeOptions() e2e.MinikubeOpts {
	var runtimes []e2e.Runtime
	if rawRuntimes, _ := os.LookupEnv("E2E_RUNTIMES"); rawRuntimes != "" {
		runtimes = []e2e.Runtime{}
	OUTER:
		for _, rawRuntime := range strings.Split(rawRuntimes, ",") {
			lower := strings.ToLower(strings.TrimSpace(rawRuntime))
			for _, runtime := range e2e.AllRuntimes {
				if lower == string(runtime) {
					runtimes = append(runtimes, runtime)
					continue OUTER
				}
			}
			log.Info().Msgf("Ignoring unknown runtime %s", rawRuntime)
		}
	} else {
		runtimes = e2e.AllRuntimes
	}

	mOpts := e2e.DefaultMinikubeOpts().WithRuntimes(runtimes...)

	if exec.Command("kvm-ok").Run() != nil {
		log.Info().Msg("KVM is not available, using docker driver")
		mOpts = mOpts.WithDriver("docker")
	} else {
		log.Info().Msg("KVM is available, using kvm driver")
		mOpts = mOpts.WithDriver("kvm2")
	}

	return mOpts
}

func testNetworkDelay(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	netperf := e2e.Netperf{Minikube: m}
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
		wantedDelay bool
	}{
		{
			name:        "should delay all traffic",
			wantedDelay: true,
		},
		{
			name:        "should delay only port 5000 traffic",
			port:        []string{"5000"},
			interfaces:  []string{"eth0"},
			wantedDelay: true,
		},
		{
			name:        "should delay only port 80 traffic",
			port:        []string{"80"},
			wantedDelay: false,
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
			Duration:     20000,
			Delay:        200,
			Jitter:       false,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction(fmt.Sprintf("%s.network_delay", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.wantedDelay {
				netperf.AssertLatency(t, unaffectedLatency+time.Duration(config.Delay)*time.Millisecond*90/100, unaffectedLatency+time.Duration(config.Delay)*time.Millisecond*350/100)
			} else {
				netperf.AssertLatency(t, 0, unaffectedLatency+40*time.Millisecond)
			}
			require.NoError(t, action.Cancel())

			netperf.AssertLatency(t, 0, unaffectedLatency+40*time.Millisecond)
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkPackageLoss(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := e2e.Iperf{Minikube: m}
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
		wantedLoss bool
	}{
		{
			name:       "should loose packages on all traffic",
			wantedLoss: true,
		},
		{
			name:       "should loose packages only on port 5001 traffic",
			port:       []string{"5001"},
			interfaces: []string{"eth0"},
			wantedLoss: true,
		},
		{
			name:       "should loose packages only on port 80 traffic",
			port:       []string{"80"},
			wantedLoss: false,
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
			Duration:     20000,
			Loss:         10,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction(fmt.Sprintf("%s.network_package_loss", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.wantedLoss {
				iperf.AssertPackageLoss(t, float64(config.Loss)*0.8, float64(config.Loss)*1.2)
			} else {
				iperf.AssertPackageLoss(t, 0, 5)
			}
			require.NoError(t, action.Cancel())

			iperf.AssertPackageLoss(t, 0, 5)
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkPackageCorruption(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := e2e.Iperf{Minikube: m}
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
		wantedCorruption bool
	}{
		{
			name:             "should corrupt packages on all traffic",
			wantedCorruption: true,
		},
		{
			name:             "should corrupt packages only on port 5001 traffic",
			port:             []string{"5001"},
			interfaces:       []string{"eth0"},
			wantedCorruption: true,
		},
		{
			name:             "should corrupt packages only on port 80 traffic",
			port:             []string{"80"},
			wantedCorruption: false,
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
			Duration:     20000,
			Corruption:   10,
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction(fmt.Sprintf("%s.network_package_corruption", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.wantedCorruption {
				iperf.AssertPackageLoss(t, float64(config.Corruption)*0.8, float64(config.Corruption)*1.2)
			} else {
				iperf.AssertPackageLoss(t, 0, 5)
			}
			require.NoError(t, action.Cancel())

			iperf.AssertPackageLoss(t, 0, 5)
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkLimitBandwidth(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	iperf := e2e.Iperf{Minikube: m}
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
		wantedLimit bool
	}{
		{
			name:        "should limit bandwidth on all traffic",
			wantedLimit: true,
		},
		{
			name:        "should limit bandwidth only on port 5001 traffic",
			port:        []string{"5001"},
			interfaces:  []string{"eth0"},
			wantedLimit: true,
		},
		{
			name:        "should limit bandwidth only on port 80 traffic",
			port:        []string{"80"},
			wantedLimit: false,
		},
	}

	unlimited, err := iperf.MeasureBandwidth()
	require.NoError(t, err)
	limited := unlimited / 3

	for _, tt := range tests {
		config := struct {
			Duration     int      `json:"duration"`
			Bandwidth    string   `json:"bandwidth"`
			Ip           []string `json:"ip"`
			Hostname     []string `json:"hostname"`
			Port         []string `json:"port"`
			NetInterface []string `json:"networkInterface"`
		}{
			Duration:     20000,
			Bandwidth:    fmt.Sprintf("%dmbit", int(limited)),
			Ip:           tt.ip,
			Hostname:     tt.hostname,
			Port:         tt.port,
			NetInterface: tt.interfaces,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction(fmt.Sprintf("%s.network_bandwidth", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if tt.wantedLimit {
				iperf.AssertBandwidth(t, limited*0.85, limited*1.15)
			} else {
				iperf.AssertBandwidth(t, unlimited*0.85, unlimited*1.15)
			}
			require.NoError(t, action.Cancel())
			iperf.AssertBandwidth(t, unlimited*0.85, unlimited*1.15)
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkBlackhole(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
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
		wantedReachable  bool
		wantedReachesUrl bool
	}{
		{
			name:             "should blackhole all traffic",
			wantedReachable:  false,
			wantedReachesUrl: false,
		},
		{
			name:             "should blackhole only port 8080 traffic",
			port:             []string{"8080"},
			wantedReachable:  true,
			wantedReachesUrl: true,
		},
		{
			name:             "should blackhole only port 80, 443 traffic",
			port:             []string{"80", "443"},
			wantedReachable:  false,
			wantedReachesUrl: false,
		},
		{
			name:             "should blackhole only traffic for steadybit.com",
			hostname:         []string{"steadybit.com"},
			wantedReachable:  true,
			wantedReachesUrl: false,
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

		hostnameBefore, err := m.PodExec(nginx.Pod, "nginx", "hostname")
		require.NoError(t, err)

		t.Run(tt.name, func(t *testing.T) {
			nginx.AssertIsReachable(t, true)
			nginx.AssertCanReach(t, "https://steadybit.com", true)

			action, err := e.RunAction(fmt.Sprintf("%s.network_blackhole", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			nginx.AssertIsReachable(t, tt.wantedReachable)
			nginx.AssertCanReach(t, "https://steadybit.com", tt.wantedReachesUrl)

			require.NoError(t, action.Cancel())
			nginx.AssertIsReachable(t, true)
			nginx.AssertCanReach(t, "https://steadybit.com", true)
		})

		hostnameAfter, err := m.PodExec(nginx.Pod, "nginx", "hostname")
		require.NoError(t, err)

		require.Equal(t, hostnameBefore, hostnameAfter, "must not alter the hostname")
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkBlackhole3Containers(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	additionalContainers := 5

	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-network-blackhole", func(pod *acorev1.PodApplyConfiguration) {
		for i := 0; i < additionalContainers; i++ {
			pod.Spec.Containers = append(pod.Spec.Containers, acorev1.ContainerApplyConfiguration{
				Name:    extutil.Ptr(fmt.Sprintf("bb-%d", i)),
				Image:   extutil.Ptr("busybox"),
				Command: []string{"sleep", "300"},
			})
		}
	})

	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	targetNginx, err := nginx.Target()
	require.NoError(t, err)
	targets := []*action_kit_api.Target{targetNginx}

	for i := 0; i < additionalContainers; i++ {
		target, err := e2e.NewContainerTarget(m, nginx.Pod, fmt.Sprintf("bb-%d", i))
		require.NoError(t, err)
		targets = append(targets, target)
	}

	config := struct {
		Duration int      `json:"duration"`
		Ip       []string `json:"ip"`
		Hostname []string `json:"hostname"`
		Port     []string `json:"port"`
	}{Duration: 10000}

	nginx.AssertIsReachable(t, true)
	nginx.AssertCanReach(t, "https://steadybit.com", true)

	executionContext := &action_kit_api.ExecutionContext{
		AgentAwsAccountId: nil,
		RestrictedEndpoints: extutil.Ptr([]action_kit_api.RestrictedEndpoint{
			{Cidr: "192.168.2.1/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.2/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.3/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.4/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.5/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.6/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.7/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.8/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.9/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.10/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.11/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.12/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.13/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.14/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.15/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "192.168.2.16/32", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a7e/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a7f/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a80/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a81/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a82/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a83/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a84/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a85/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a86/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a87/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a88/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a89/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a8a/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a8b/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a8c/128", PortMin: 8086, PortMax: 8088},
			{Cidr: "fe80::70c4:51ff:fe20:3a8e/128", PortMin: 8086, PortMax: 8088},
		}),
	}

	chActions := make(chan client.ActionExecution, len(targets))
	chErrors := make(chan error, len(targets))
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(target *action_kit_api.Target) {
			defer wg.Done()
			action, err := e.RunAction(fmt.Sprintf("%s.network_blackhole", extcontainer.BaseActionID), target, config, executionContext)
			chActions <- action
			if err != nil {
				chErrors <- err
			}
		}(t)
	}
	wg.Wait()
	close(chActions)

	var actions []client.ActionExecution
	for a := range chActions {
		actions = append(actions, a)
	}
	for _, a := range actions {
		defer func(action client.ActionExecution) { _ = action.Cancel() }(a)
	}

	require.Empty(t, chErrors)

	nginx.AssertIsReachable(t, false)
	nginx.AssertCanReach(t, "https://steadybit.com", false)

	wg = sync.WaitGroup{}
	for _, a := range actions {
		wg.Add(1)
		go func(action client.ActionExecution) {
			defer wg.Done()
			if err := action.Cancel(); err != nil {
				chErrors <- err
			}
		}(a)
	}

	wg.Wait()
	require.Empty(t, chErrors)

	nginx.AssertIsReachable(t, true)
	nginx.AssertCanReach(t, "https://steadybit.com", true)
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkBlockDns(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
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
		wantedReachable  bool
		wantedReachesUrl bool
	}{
		{
			name:             "should block dns traffic",
			dnsPort:          53,
			wantedReachable:  true,
			wantedReachesUrl: false,
		},
		{
			name:             "should block dns traffic on port 5353",
			dnsPort:          5353,
			wantedReachable:  true,
			wantedReachesUrl: true,
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
			nginx.AssertIsReachable(t, true)
			nginx.AssertCanReach(t, "https://steadybit.com", true)

			action, err := e.RunAction(fmt.Sprintf("%s.network_block_dns", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			nginx.AssertIsReachable(t, tt.wantedReachable)
			if tt.wantedReachesUrl {
				nginx.AssertCanReach(t, "https://steadybit.com", true)
			} else {
				nginx.AssertCannotReach(t, "https://steadybit.com", "Resolving timed out after")
			}
			require.NoError(t, action.Cancel())
			nginx.AssertIsReachable(t, true)
			nginx.AssertCanReach(t, "https://steadybit.com", true)
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testStressCpu(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	nginx := e2e.Nginx{Minikube: m}
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

	hostnameBefore, err := m.PodExec(nginx.Pod, "nginx", "hostname")
	require.NoError(t, err)

	action, err := e.RunAction(fmt.Sprintf("%s.stress_cpu", extcontainer.BaseActionID), target, config, executionContext)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)
	e2e.AssertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng", false)
	require.NoError(t, action.Cancel())
	e2e.AssertProcessNOTRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")

	hostnameAfter, err := m.PodExec(nginx.Pod, "nginx", "hostname")
	require.NoError(t, err)

	require.Equal(t, hostnameBefore, hostnameAfter, "must not alter the hostname")
	requireAllSidecarsCleanedUp(t, m, e)
}

func testStressMemory(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	tests := []struct {
		name          string
		failOnOomKill bool
		performKill   bool
		wantedErr     *string
	}{
		{
			name:          "should perform successfully",
			failOnOomKill: false,
			performKill:   false,
			wantedErr:     nil,
		}, {
			name:          "should fail on oom kill",
			failOnOomKill: true,
			performKill:   true,
			wantedErr:     extutil.Ptr("exit status 137"),
		}, {
			name:          "should not fail on oom kill",
			failOnOomKill: false,
			performKill:   true,
			wantedErr:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nginx := e2e.Nginx{Minikube: m}
			err := nginx.Deploy("nginx-stress-mem", func(p *acorev1.PodApplyConfiguration) {
				p.Spec.Containers[0].Resources = &acorev1.ResourceRequirementsApplyConfiguration{
					Limits: &corev1.ResourceList{
						"memory": resource.MustParse("50Mi"),
					},
				}
			})
			require.NoError(t, err, "failed to create pod")
			defer func() { _ = nginx.Delete() }()

			target, err := nginx.Target()
			require.NoError(t, err)

			config := struct {
				Duration      int  `json:"duration"`
				Percentage    int  `json:"percentage"`
				FailOnOomKill bool `json:"failOnOomKill"`
			}{Duration: 10000, Percentage: 10, FailOnOomKill: tt.failOnOomKill}

			action, err := e.RunAction(fmt.Sprintf("%s.stress_mem", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			e2e.AssertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng", false)

			if tt.performKill {
				println("performing kill")
				require.NoError(t, m.SshExec("sudo pkill -9 stress-ng").Run())
			}

			if tt.wantedErr == nil {
				require.NoError(t, action.Cancel())
			} else {
				err := action.Wait()
				require.ErrorContains(t, err, *tt.wantedErr)
			}
			e2e.AssertProcessNOTRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testStressIo(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-stress-io", func(c *acorev1.PodApplyConfiguration) {
		c.Spec.Containers[0].VolumeMounts = []acorev1.VolumeMountApplyConfiguration{
			{
				Name:      extutil.Ptr("host-tmp"),
				MountPath: extutil.Ptr("/host-tmp"),
			},
		}
		c.Spec.Volumes = []acorev1.VolumeApplyConfiguration{
			{
				Name: extutil.Ptr("host-tmp"),
				VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
					HostPath: &acorev1.HostPathVolumeSourceApplyConfiguration{
						Path: extutil.Ptr("/tmp"),
					},
				},
			},
		}
	})
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	_, err = m.PodExec(nginx.Pod, "nginx", "mkdir", "-p", "/host-tmp/stressng")
	require.NoError(t, err)

	target, err := nginx.Target()
	require.NoError(t, err)

	for _, mode := range []string{"read_write_and_flush", "read_write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			config := struct {
				Duration        int    `json:"duration"`
				Path            string `json:"path"`
				MbytesPerWorker int    `json:"mbytes_per_worker"`
				Workers         int    `json:"workers"`
				Mode            string `json:"mode"`
			}{Duration: 20000, Workers: 1, MbytesPerWorker: 50, Path: "/host-tmp/stressng", Mode: mode}

			action, err := e.RunAction(fmt.Sprintf("%s.stress_io", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)
			e2e.AssertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng", false)
			require.NoError(t, action.Cancel())
			e2e.AssertProcessNOTRunningInContainer(t, m, nginx.Pod, "nginx", "stress-ng")

			out, err := m.PodExec(nginx.Pod, "nginx", "ls", "/host-tmp/stressng")
			require.NoError(t, err)
			space := strings.TrimSpace(out)
			require.Empty(t, space, "no stress-ng directories must be present")
		})
	}

	requireAllSidecarsCleanedUp(t, m, e)
}

func testFillDisk(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-fill-disk", func(c *acorev1.PodApplyConfiguration) {
		c.Spec.Containers[0].VolumeMounts = []acorev1.VolumeMountApplyConfiguration{
			{
				Name:      extutil.Ptr("host-tmp"),
				MountPath: extutil.Ptr("/host-tmp"),
			},
		}
		c.Spec.Volumes = []acorev1.VolumeApplyConfiguration{
			{
				Name: extutil.Ptr("host-tmp"),
				VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
					HostPath: &acorev1.HostPathVolumeSourceApplyConfiguration{
						Path: extutil.Ptr("/tmp"),
					},
				},
			},
		}
	})
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	_, err = m.PodExec(nginx.Pod, "nginx", "mkdir", "-p", "/host-tmp/filldisk")
	require.NoError(t, err)

	target, err := nginx.Target()
	require.NoError(t, err)

	type testCase struct {
		name           string
		mode           string
		size           int
		blockSize      int
		method         string
		wantedFileSize int
		wantedDelta    int
	}
	testCases := []testCase{
		{
			name:           "fill disk with percentage (fallocate)",
			mode:           "PERCENTAGE",
			size:           50,
			blockSize:      0,
			method:         "AT_ONCE",
			wantedFileSize: 3 * 1024,
			wantedDelta:    512,
		},
		{
			name:           "fill disk with megabytes to fill (fallocate)",
			mode:           "MB_TO_FILL",
			size:           4 * 1024, // 4GB
			blockSize:      0,
			method:         "AT_ONCE",
			wantedFileSize: 4 * 1024,
			wantedDelta:    0,
		},
		{
			name:           "fill disk with megabytes left (fallocate)",
			mode:           "MB_LEFT",
			size:           4 * 1024, // 4GB
			blockSize:      0,
			method:         "AT_ONCE",
			wantedFileSize: 3 * 1024,
			wantedDelta:    1.5 * 1024,
		},
		{
			name:           "fill disk with percentage (dd)",
			mode:           "PERCENTAGE",
			size:           50,
			blockSize:      1024,
			method:         "OVER_TIME",
			wantedFileSize: 3 * 1024,
			wantedDelta:    1024,
		},
		{
			name:           "fill disk with megabytes to fill (dd)",
			mode:           "MB_TO_FILL",
			size:           4 * 1024, // 4GB
			blockSize:      1024,
			method:         "OVER_TIME",
			wantedFileSize: 4 * 1024,
			wantedDelta:    0,
		},
		{
			name:           "fill disk with megabytes left (dd)",
			mode:           "MB_LEFT",
			size:           4 * 1024, // 4GB
			wantedFileSize: 3 * 1024,
			method:         "OVER_TIME",
			wantedDelta:    1.5 * 1024,
			blockSize:      1024,
		},
		{
			name:           "fill disk with bigger blocksize (dd)",
			mode:           "MB_TO_FILL",
			size:           4 * 1024, // 4GB
			blockSize:      6 * 1024, // 2GB
			method:         "OVER_TIME",
			wantedFileSize: 4 * 1024, // 4GB
			wantedDelta:    0,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := struct {
				Duration  int    `json:"duration"`
				Path      string `json:"path"`
				Size      int    `json:"size"`
				Mode      string `json:"mode"`
				BlockSize int    `json:"blocksize"`
				Method    string `json:"method"`
			}{Duration: 60000, Size: testCase.size, Mode: testCase.mode, Method: testCase.method, BlockSize: testCase.blockSize, Path: "/host-tmp/filldisk"}

			action, err := e.RunAction(fmt.Sprintf("%s.fill_disk", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()
			require.NoError(t, err)

			if testCase.method == "OVER_TIME" {
				e2e.AssertProcessRunningInContainer(t, m, nginx.Pod, "nginx", "dd", false)
			}
			assertFileHasSize(t, m, nginx.Pod, "nginx", "/host-tmp/filldisk/disk-fill", testCase.wantedFileSize, testCase.wantedDelta)
			require.NoError(t, action.Cancel())

			if testCase.method == "OVER_TIME" {
				e2e.AssertProcessNOTRunningInContainer(t, m, nginx.Pod, "nginx", "dd")
			} else {
				e2e.AssertProcessNOTRunningInContainer(t, m, nginx.Pod, "nginx", "fallocate")
			}

			out, _ := m.PodExec(nginx.Pod, "nginx", "ls", "/host-tmp/filldisk/disk-fill")
			assert.Contains(t, string(out), "No such file or directory")
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testPauseContainer(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" {
		t.Skip("pause is not supported in cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
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
		require.NoError(t, e2e.WaitForContainerStatusUsingContainerEngine(m, status.ContainerID, "paused"))
		ts <- time.Now()
		require.NoError(t, e2e.WaitForContainerStatusUsingContainerEngine(m, status.ContainerID, "running"))
		ts <- time.Now()
	}()

	config := struct {
		Duration int `json:"duration"`
	}{Duration: 5000}
	action, err := e.RunAction(fmt.Sprintf("%s.pause", extcontainer.BaseActionID), target, config, executionContext)
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
func testStopContainer(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	nginx := e2e.Nginx{Minikube: m}
	nginx2 := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-stop")
	require.NoError(t, err, "failed to create pod")
	err = nginx2.Deploy("nginx-stop-2")
	require.NoError(t, err, "failed to create pod 2")
	defer func() { _ = nginx.Delete() }()
	defer func() { _ = nginx2.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)
	target2, err := nginx2.Target()
	require.NoError(t, err)

	config := struct {
		Graceful bool `json:"graceful"`
	}{Graceful: true}
	go func() {
		action, err := e.RunAction(fmt.Sprintf("%s.stop", extcontainer.BaseActionID), target, config, executionContext)
		defer func() { _ = action.Cancel() }()
		require.NoError(t, err)
		require.NoError(t, action.Wait())
	}()
	action2, err2 := e.RunAction(fmt.Sprintf("%s.stop", extcontainer.BaseActionID), target2, config, executionContext)

	defer func() { _ = action2.Cancel() }()
	require.NoError(t, err2)
	require.NoError(t, action2.Wait())

	require.NoError(t, m.WaitForPodPhase(nginx.Pod, corev1.PodSucceeded, 30*time.Second))
	require.NoError(t, m.WaitForPodPhase(nginx2.Pod, corev1.PodSucceeded, 30*time.Second))

	status, err := nginx.ContainerStatus()
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.NotNil(t, status.State.Terminated, "container should be terminated")

	status2, err := nginx2.ContainerStatus()
	require.NoError(t, err)
	require.NotNil(t, status2)
	assert.NotNil(t, status2.State.Terminated, "container should be terminated")
}

func validateDiscovery(t *testing.T, _ *e2e.Minikube, e *e2e.Extension) {
	assert.NoError(t, validate.ValidateEndpointReferences("/", e.Client))
}

func testDiscovery(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-discovery")
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	target, err := e2e.PollForTarget(ctx, e, "com.steadybit.extension_container.container", func(target discovery_kit_api.Target) bool {
		return e2e.HasAttribute(target, "k8s.pod.name", "nginx-discovery")
	})
	require.NoError(t, err)
	assert.Equal(t, target.TargetType, "com.steadybit.extension_container.container")
	assert.NotContains(t, target.Attributes, "container.label.maintainer")
	targets, err := e.DiscoverTargets("com.steadybit.extension_container.container")
	require.NoError(t, err)
	for _, target := range targets {
		for _, img := range target.Attributes["container.image"] {
			assert.NotContains(t, img, "pause", "pause container should not be discovered")
		}
	}
}

func testHostNetwork(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-network-host", func(pod *acorev1.PodApplyConfiguration) {
		pod.Spec.HostNetwork = extutil.Ptr(true)
	})
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)

	tests := []struct {
		name              string
		failOnHostNetwork bool
		wantedError       bool
	}{
		{
			name:              "should fail with host network",
			failOnHostNetwork: true,
			wantedError:       true,
		},
		{
			name:              "should allow host network",
			failOnHostNetwork: false,
			wantedError:       false,
		},
	}

	for _, tt := range tests {
		config := struct {
			Duration          int      `json:"duration"`
			FailOnHostNetwork bool     `json:"failOnHostNetwork"`
			Port              []string `json:"port"`
		}{
			Duration:          10000,
			Port:              []string{"80"},
			FailOnHostNetwork: tt.failOnHostNetwork,
		}

		t.Run(tt.name, func(t *testing.T) {
			action, err := e.RunAction(fmt.Sprintf("%s.network_blackhole", extcontainer.BaseActionID), target, config, executionContext)
			defer func() { _ = action.Cancel() }()

			if tt.wantedError {
				require.ErrorContains(t, err, "Container is using host network")
			} else {
				require.NoError(t, err)
				require.NoError(t, action.Cancel())
			}
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

func testNetworkDelayOnTwoContainers(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
	err := nginx.Deploy("nginx-double", func(pod *acorev1.PodApplyConfiguration) {
		pod.Spec.Containers = append(pod.Spec.Containers, acorev1.ContainerApplyConfiguration{
			Name:    extutil.Ptr("sleeper"),
			Image:   extutil.Ptr("alpine:latest"),
			Command: []string{"sleep", "10000"},
		},
		)
	})
	require.NoError(t, err, "failed to create pod")
	defer func() { _ = nginx.Delete() }()

	target, err := nginx.Target()
	require.NoError(t, err)
	target2, err := e2e.NewContainerTarget(m, nginx.Pod, "sleeper")
	require.NoError(t, err)

	config := struct {
		Duration int `json:"duration"`
		Delay    int `json:"networkDelay"`
	}{
		Duration: 10000,
		Delay:    200,
	}

	action, err := e.RunAction(fmt.Sprintf("%s.network_delay", extcontainer.BaseActionID), target, config, executionContext)
	defer func() { _ = action.Cancel() }()
	require.NoError(t, err)

	action2, err2 := e.RunAction(fmt.Sprintf("%s.network_delay", extcontainer.BaseActionID), target2, config, executionContext)
	defer func() { _ = action2.Cancel() }()
	require.NoError(t, err2)

	requireAllSidecarsCleanedUp(t, m, e)
}

func requireAllSidecarsCleanedUp(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	out, err := m.PodExec(e.Pod, "steadybit-extension-container", "ls", "/tmp/steadybit/containers")
	require.NoError(t, err)
	space := strings.TrimSpace(out)
	require.Empty(t, space, "no sidecar directories must be present")
}

func assertFileHasSize(t *testing.T, m *e2e.Minikube, pod metav1.Object, containername string, filepath string, wantedSizeInMb int, wantedDeltaInMb int) {
	sizeInBytes := wantedSizeInMb * 1024 * 1024
	deltaInBytes := wantedDeltaInMb * 1024 * 1024
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	message := ""
	for {
		select {
		case <-ctx.Done():
			assert.Fail(t, "file has not the expected size", message)
			return

		case <-time.After(200 * time.Millisecond):
			out, err := m.PodExec(pod, containername, "stat", "-c", "%s", filepath)
			if err != nil {
				message = fmt.Sprintf("%s: %s", err.Error(), out)
				continue
			}

			if fileSize, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				actualDelta := int(math.Abs(float64(fileSize - sizeInBytes)))
				if actualDelta <= deltaInBytes {
					return
				} else {
					message = fmt.Sprintf("file size is %d, wanted %d, delta of %d exceeds allowed delta of %d", fileSize, sizeInBytes, actualDelta, deltaInBytes)
				}
			} else {
				message = fmt.Sprintf("cannot parse file size: %s", err.Error())
			}
		}
	}
}
