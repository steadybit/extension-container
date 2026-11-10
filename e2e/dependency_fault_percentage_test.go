// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	"github.com/steadybit/extension-container/extcontainer"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testNetworkDependencyFaultPercentage checks that Percentage applies per
// request on a keep-alive connection: on cleartext HTTP, and on HTTPS that the
// proxy decrypts with the interception CA.
func testNetworkDependencyFaultPercentage(t *testing.T, m *e2e.Minikube, e *e2e.Extension) {
	if m.Runtime == "cri-o" && m.Driver == "docker" {
		t.Skip("Due to https://github.com/kubernetes/minikube/issues/16371 this test is skipped for cri-o")
	}

	nginx := e2e.Nginx{Minikube: m}
	require.NoError(t, nginx.Deploy("nginx-dependency-percentage"), "failed to create pod")
	defer func() { _ = nginx.Delete() }()
	target, err := nginx.Target()
	require.NoError(t, err)

	// The suite configures an interception CA; the target trusts it over HTTPS,
	// as a customer's workload would.
	_, err = m.PodExec(nginx.Pod, "nginx", "sh", "-c",
		fmt.Sprintf("cat > /tmp/intercept-ca.crt <<'PEMEOF'\n%s\nPEMEOF", string(interceptCA.certPEM)))
	require.NoError(t, err, "failed to place the CA in the target")

	const host = "steadybit.com"

	tests := []struct {
		name, port, url string
		curlArgs        []string
	}{
		{name: "cleartext HTTP", port: "80", url: "http://" + host},
		{name: "HTTPS through the interception CA", port: "443", url: "https://" + host, curlArgs: []string{"--cacert", "/tmp/intercept-ca.crt"}},
	}
	for _, tt := range tests {
		t.Run("intercept applies the percentage per request on a keep-alive connection over "+tt.name, func(t *testing.T) {
			config := map[string]any{
				"duration": 60000, "hostname": []string{host},
				"percentage": 50, "port": tt.port, "httpStatus": 503,
			}
			action, err := e.RunAction(fmt.Sprintf("%s.network_dependency_http_response", extcontainer.BaseActionID), target, config, &action_kit_api.ExecutionContext{})
			require.NoError(t, err)
			defer func() { _ = action.Cancel() }()

			// 100 requests over one curl connection. A per-connection roll would fault
			// all or none of them; per request, about half are faulted. 30..70 is
			// about 4 standard deviations at n=100.
			const requests = 100
			e2e.Retry(t, 10, 1*time.Second, func(r *e2e.R) {
				got, err := curlKeepAlive(m, nginx.Pod, tt.url, requests, tt.curlArgs...)
				if err != nil {
					r.Failed = true
					_, _ = fmt.Fprintf(r.Log, "requests failed: %v", err)
					return
				}
				if got.responses != requests || got.faulted < 30 || got.faulted > 70 || got.connects > 5 {
					r.Failed = true
					_, _ = fmt.Fprintf(r.Log, "expected %d responses, about half faulted, over one connection; got %d responses, %d faulted, over %d connections",
						requests, got.responses, got.faulted, got.connects)
				}
			})
			require.NoError(t, action.Cancel())
		})
	}
	requireAllSidecarsCleanedUp(t, m, e)
}

// curlKeepAlive sends n GET requests to url in a single curl call, which keeps
// one connection open for all of them, and summarizes the responses. extraArgs
// go before the URLs, e.g. --cacert for HTTPS.
func curlKeepAlive(m *e2e.Minikube, pod metav1.Object, url string, n int, extraArgs ...string) (keepAliveResult, error) {
	args := append([]string{"curl", "--max-time", "30", "-s", "-w", "%{http_code} %{num_connects}\n"}, extraArgs...)
	for range n {
		args = append(args, "-o", "/dev/null", url)
	}
	out, err := m.PodExec(pod, "nginx", args...)
	if err != nil {
		return keepAliveResult{}, err
	}
	return parseCurlWriteOut(out), nil
}

// keepAliveResult summarizes curlKeepAlive: how many responses arrived, how
// many were the injected 503, and how many connections curl had to open.
type keepAliveResult struct {
	responses, faulted, connects int
}

// parseCurlWriteOut reads curl's "-w '%{http_code} %{num_connects}\n'" output.
// PodExec runs with a TTY, which ends every line with "\r\n"; splitting into
// fields drops the "\r" that would otherwise make every count unparsable.
func parseCurlWriteOut(out string) keepAliveResult {
	var r keepAliveResult
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		r.responses++
		if fields[0] == "503" {
			r.faulted++
		}
		if c, err := strconv.Atoi(fields[1]); err == nil {
			r.connects += c
		}
	}
	return r
}
