// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package upstreamtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
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

func TestParameter(t *testing.T) {
	p := Parameter(16)
	require.Equal(t, ParameterName, p.Name)
	require.Equal(t, "false", *p.DefaultValue)
	require.True(t, *p.Advanced)
	require.Contains(t, *p.Description, "Unsafe")
}

func TestSkipVerify(t *testing.T) {
	on := map[string]any{ParameterName: true}
	require.True(t, SkipVerify(on, &proxyfault.TLSInterceptCA{}))
	require.False(t, SkipVerify(map[string]any{}, &proxyfault.TLSInterceptCA{}), "verified by default")
	// Without a CA the proxy never opens its own TLS connection.
	require.False(t, SkipVerify(on, nil))
}

func TestCustomCAs(t *testing.T) {
	certPEM := testCertPEM(t)
	caDir, emptyDir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(caDir+"/company-ca.crt", certPEM, 0600))
	caFile := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(caFile, certPEM, 0600))
	notPEM := t.TempDir() + "/ca.pem"
	require.NoError(t, os.WriteFile(notPEM, []byte("not a certificate"), 0600))

	t.Setenv("SSL_CERT_DIR", caDir+":"+emptyDir+":/nonexistent/certs")
	t.Setenv("SSL_CERT_FILE", caFile)
	valid, invalid := CustomCAs()
	require.Equal(t, []string{caDir, caFile}, valid)
	require.Equal(t, []string{emptyDir, "/nonexistent/certs"}, invalid)

	t.Setenv("SSL_CERT_DIR", "")
	t.Setenv("SSL_CERT_FILE", notPEM)
	valid, invalid = CustomCAs()
	require.Empty(t, valid)
	require.Equal(t, []string{notPEM}, invalid)

	t.Setenv("SSL_CERT_FILE", "")
	valid, invalid = CustomCAs()
	require.Empty(t, valid)
	require.Empty(t, invalid)
}

func TestMessages(t *testing.T) {
	// Not decrypted: no upstream TLS to warn about.
	require.Empty(t, Messages(proxyfault.Opts{}, nil, []string{"/missing"}))

	// Skipping verification is unsafe, and makes the CAs irrelevant.
	msgs := Messages(skipping, nil, []string{"/missing"})
	require.Len(t, msgs, 1)
	require.Equal(t, action_kit_api.Warn, *msgs[0].Level)
	require.Contains(t, msgs[0].Message, "Unsafe: intercepted HTTPS requests are forwarded to the dependency without verifying its certificate")

	msgs = Messages(decrypting, nil, nil)
	require.Len(t, msgs, 1)
	require.Equal(t, action_kit_api.Warn, *msgs[0].Level)
	require.Contains(t, msgs[0].Message, "Only public CAs")
	require.Contains(t, msgs[0].Message, "Skip Dependency Certificate Verification")

	msgs = Messages(decrypting, []string{"/etc/ssl/certs"}, []string{"/etc/ssl/extra-certs"})
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Message, "/etc/ssl/extra-certs")
	require.Contains(t, msgs[0].Message, "holds no PEM certificate")
}

func TestErrorTip(t *testing.T) {
	require.Nil(t, ErrorTip(decrypting, nil, 0))
	// Without decryption, or without verification, a failure is a connection
	// failure only; the widget reports it.
	require.Nil(t, ErrorTip(proxyfault.Opts{}, nil, 3))
	require.Nil(t, ErrorTip(skipping, nil, 3))

	tip := ErrorTip(decrypting, nil, 3)
	require.NotNil(t, tip)
	require.Equal(t, action_kit_api.Warn, *tip.Level)
	require.Contains(t, tip.Message, "failed to forward 3 time(s)")
	require.Contains(t, tip.Message, "Only public CAs")
	require.Contains(t, tip.Message, "Skip Dependency Certificate Verification")

	tip = ErrorTip(decrypting, []string{"/etc/ssl/extra-certs"}, 3)
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
