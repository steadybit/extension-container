// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extcontainer

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/proxyfault"
	"github.com/stretchr/testify/require"
)

func Test_upstreamCustomCAs(t *testing.T) {
	certPEM, _ := testCAPEM(t)
	caDir, emptyDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(caDir+"/company-ca.crt", certPEM, 0600))
	caFile := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(caFile, certPEM, 0600))
	notPEM := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(notPEM, []byte("not a certificate"), 0600))

	t.Setenv("SSL_CERT_DIR", caDir+":"+emptyDir+":/nonexistent/certs")
	t.Setenv("SSL_CERT_FILE", caFile)
	valid, invalid := upstreamCustomCAs()
	require.Equal(t, []string{caDir, caFile}, valid)
	require.Equal(t, []string{emptyDir, "/nonexistent/certs"}, invalid)

	t.Setenv("SSL_CERT_DIR", "")
	t.Setenv("SSL_CERT_FILE", notPEM)
	valid, invalid = upstreamCustomCAs()
	require.Empty(t, valid)
	require.Equal(t, []string{notPEM}, invalid)

	t.Setenv("SSL_CERT_FILE", "")
	valid, invalid = upstreamCustomCAs()
	require.Empty(t, valid)
	require.Empty(t, invalid)
}

func Test_upstreamTrustMessages(t *testing.T) {
	decrypting := proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}}

	// Not decrypted, or not verified: no upstream TLS to warn about.
	require.Empty(t, upstreamTrustMessages(proxyfault.Opts{}, nil, nil))
	require.Empty(t, upstreamTrustMessages(proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}, UpstreamInsecureSkipVerify: true}, nil, []string{"/missing"}))

	msgs := upstreamTrustMessages(decrypting, nil, nil)
	require.Len(t, msgs, 1)
	require.Equal(t, action_kit_api.Warn, *msgs[0].Level)
	require.Contains(t, msgs[0].Message, "Only public CAs")
	require.Contains(t, msgs[0].Message, "Skip Dependency Certificate Verification")

	msgs = upstreamTrustMessages(decrypting, []string{"/etc/ssl/certs"}, []string{"/etc/ssl/extra-certs"})
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Message, "/etc/ssl/extra-certs")
	require.Contains(t, msgs[0].Message, "holds no PEM certificate")
}

func Test_upstreamErrorTip(t *testing.T) {
	decrypting := proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}}

	require.Nil(t, upstreamErrorTip(decrypting, nil, 0))
	// Without decryption, or without verification, a failure is a connection
	// failure only; the widget reports it.
	require.Nil(t, upstreamErrorTip(proxyfault.Opts{}, nil, 3))
	require.Nil(t, upstreamErrorTip(proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}, UpstreamInsecureSkipVerify: true}, nil, 3))

	tip := upstreamErrorTip(decrypting, nil, 3)
	require.NotNil(t, tip)
	require.Equal(t, action_kit_api.Warn, *tip.Level)
	require.Contains(t, tip.Message, "failed to forward 3 time(s)")
	require.Contains(t, tip.Message, "Only public CAs")
	require.Contains(t, tip.Message, "Skip Dependency Certificate Verification")

	tip = upstreamErrorTip(decrypting, []string{"/etc/ssl/extra-certs"}, 3)
	require.Contains(t, tip.Message, "Custom CAs are configured (/etc/ssl/extra-certs), but they may not include")
}

type upstreamErrorsProxy struct {
	fakeProxy
	errors int64
}

func (p upstreamErrorsProxy) Metrics() (proxyfault.Snapshot, bool) {
	return proxyfault.Snapshot{UpstreamErrors: p.errors}, true
}

func Test_dependencyFault_Status_logsUpstreamErrorTipOnce(t *testing.T) {
	executionId := uuid.NewString()
	storeDependencyFaultHandle(executionId, &proxyHandle{
		proxy: upstreamErrorsProxy{errors: 2},
		opts:  proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}},
	})
	t.Cleanup(func() { removeDependencyFaultHandle(executionId) })
	a := &dependencyFaultAction{spec: latencyFaultSpec}

	tips := func() int {
		res, err := a.Status(context.Background(), &DependencyFaultState{ExecutionId: executionId})
		require.NoError(t, err)
		n := 0
		for _, m := range *res.Messages {
			if strings.Contains(m.Message, "failed to forward") {
				n++
			}
		}
		return n
	}
	require.Equal(t, 1, tips())
	require.Equal(t, 0, tips(), "the tip is logged once per execution")
}

func Test_formatDependencyStatsMarkdown_upstreamErrors(t *testing.T) {
	md := formatDependencyStatsMarkdown(proxyfault.Snapshot{ConnectionsMatched: 1}, proxyfault.Opts{})
	require.NotContains(t, md, "Forwarding failed")

	md = formatDependencyStatsMarkdown(proxyfault.Snapshot{ConnectionsMatched: 1, UpstreamErrors: 2}, proxyfault.Opts{})
	require.Contains(t, md, "Forwarding failed 2 time(s).** The proxy couldn't connect to the destination._")

	md = formatDependencyStatsMarkdown(proxyfault.Snapshot{ConnectionsMatched: 1, UpstreamErrors: 2}, proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}})
	require.Contains(t, md, "or couldn't verify the dependency's certificate")
}
