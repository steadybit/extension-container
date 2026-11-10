// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package flags

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/proxyfault"
	"github.com/stretchr/testify/require"
)

var (
	decrypting = proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}}
	skipping   = proxyfault.Opts{TLSInterceptCA: &proxyfault.TLSInterceptCA{}, UpstreamInsecureSkipVerify: true}
)

func TestUpstreamSkipVerifyParameter(t *testing.T) {
	p := UpstreamSkipVerifyParameter(16, "Traffic Filter")
	require.Equal(t, "Traffic Filter", *p.Group)
	require.Equal(t, UpstreamSkipVerifyParameterName, p.Name)
	require.Equal(t, "false", *p.DefaultValue)
	require.True(t, *p.Advanced)
	require.True(t, strings.HasPrefix(*p.Description, "Unsafe, test environments only: "), *p.Description)
}

func TestUpstreamSkipVerify(t *testing.T) {
	on := map[string]any{UpstreamSkipVerifyParameterName: true}
	require.True(t, UpstreamSkipVerify(on, &proxyfault.TLSInterceptCA{}))
	require.False(t, UpstreamSkipVerify(map[string]any{}, &proxyfault.TLSInterceptCA{}), "verified by default")
	// Without a CA the proxy never opens its own TLS connection.
	require.False(t, UpstreamSkipVerify(on, nil))
}

func TestUpstreamCustomCAs(t *testing.T) {
	certPEM := testCertPEM(t)
	caDir, emptyDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(caDir+"/company-ca.crt", certPEM, 0600))
	caFile := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(caFile, certPEM, 0600))
	notPEM := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(notPEM, []byte("not a certificate"), 0600))

	t.Setenv("SSL_CERT_DIR", caDir+":"+emptyDir+":/nonexistent/certs")
	t.Setenv("SSL_CERT_FILE", caFile)
	valid, invalid := UpstreamCustomCAs()
	require.Equal(t, []string{caDir, caFile}, valid)
	require.Equal(t, []string{emptyDir, "/nonexistent/certs"}, invalid)

	t.Setenv("SSL_CERT_DIR", "")
	t.Setenv("SSL_CERT_FILE", notPEM)
	valid, invalid = UpstreamCustomCAs()
	require.Empty(t, valid)
	require.Equal(t, []string{notPEM}, invalid)

	t.Setenv("SSL_CERT_FILE", "")
	valid, invalid = UpstreamCustomCAs()
	require.Empty(t, valid)
	require.Empty(t, invalid)
}

func TestUpstreamTLSMessages(t *testing.T) {
	// Not decrypted: no upstream TLS to warn about.
	require.Empty(t, UpstreamTLSMessages(proxyfault.Opts{}, nil, []string{"/missing"}))

	// Skipping verification is unsafe, and makes the CAs irrelevant.
	msgs := UpstreamTLSMessages(skipping, nil, []string{"/missing"})
	require.Len(t, msgs, 1)
	require.Equal(t, action_kit_api.Warn, *msgs[0].Level)
	require.True(t, strings.HasPrefix(msgs[0].Message, "Unsafe, test environments only: intercepted HTTPS requests are forwarded to the dependency without verifying its certificate"), msgs[0].Message)
	require.Contains(t, msgs[0].Message, "including any credentials")

	msgs = UpstreamTLSMessages(decrypting, nil, nil)
	require.Len(t, msgs, 1)
	require.Equal(t, action_kit_api.Warn, *msgs[0].Level)
	require.Contains(t, msgs[0].Message, "Only public CAs")
	require.Contains(t, msgs[0].Message, "Skip Dependency Certificate Verification")

	msgs = UpstreamTLSMessages(decrypting, []string{"/etc/ssl/certs"}, []string{"/etc/ssl/extra-certs"})
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Message, "/etc/ssl/extra-certs")
	require.Contains(t, msgs[0].Message, "holds no PEM certificate")
}

func TestUpstreamErrorTip(t *testing.T) {
	require.Nil(t, UpstreamErrorTip(decrypting, nil, 0))
	// Without decryption, or without verification, a failure is a connection
	// failure only; the widget reports it.
	require.Nil(t, UpstreamErrorTip(proxyfault.Opts{}, nil, 3))
	require.Nil(t, UpstreamErrorTip(skipping, nil, 3))

	tip := UpstreamErrorTip(decrypting, nil, 3)
	require.NotNil(t, tip)
	require.Equal(t, action_kit_api.Warn, *tip.Level)
	require.Contains(t, tip.Message, "failed to forward 3 time(s)")
	require.Contains(t, tip.Message, "Only public CAs")
	require.Contains(t, tip.Message, "Skip Dependency Certificate Verification")

	tip = UpstreamErrorTip(decrypting, []string{"/etc/ssl/extra-certs"}, 3)
	require.Contains(t, tip.Message, "Custom CAs are configured (/etc/ssl/extra-certs), but they may not include")
}

func testCertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
