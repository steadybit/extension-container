// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

// Package flags holds the action parameters that change what an attack does,
// with the logic and the execution-log output that depend on them.
package flags

// The proxy's TLS connection to the dependency when it forwards a decrypted
// HTTPS request: the experiment setting to skip verifying the dependency's
// certificate, the custom CAs it is verified against, and the warnings for
// when that is unsafe or can fail.

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/proxyfault"
	"github.com/steadybit/extension-kit/extutil"
)

const UpstreamSkipVerifyParameterName = "upstreamInsecureSkipVerify"

// UpstreamSkipVerifyParameter lets an experiment forward intercepted HTTPS
// requests to a dependency whose certificate the proxy cannot verify. It is no
// traffic filter, so it is shown inline, next to the other toggles.
func UpstreamSkipVerifyParameter(order int) action_kit_api.ActionParameter {
	return action_kit_api.ActionParameter{
		Name:        UpstreamSkipVerifyParameterName,
		Label:       "Skip Dependency Certificate Verification",
		Description: extutil.Ptr("Unsafe, test environments only: forward intercepted HTTPS requests to the dependency without verifying its certificate, e.g. a self-signed or private-CA one."),
		Type:        action_kit_api.ActionParameterTypeBoolean, DefaultValue: extutil.Ptr("false"), Required: extutil.Ptr(true), Order: extutil.Ptr(order),
	}
}

// UpstreamSkipVerify reads the setting. It only applies to decrypted HTTPS, so
// it is off without an interception CA.
func UpstreamSkipVerify(cfg map[string]any, interceptCA *proxyfault.TLSInterceptCA) bool {
	return interceptCA != nil && extutil.ToBool(cfg[UpstreamSkipVerifyParameterName])
}

// VerifiesUpstream reports whether the proxy forwards decrypted HTTPS and
// verifies the dependency's certificate on the way.
func VerifiesUpstream(opts proxyfault.Opts) bool {
	return opts.TLSInterceptCA != nil && !opts.UpstreamInsecureSkipVerify
}

// UpstreamCustomCAs sorts the custom CA paths the proxy inherits from the
// extension (SSL_CERT_DIR / SSL_CERT_FILE, the exttls convention) into those
// holding at least one PEM certificate and those that don't.
func UpstreamCustomCAs() (valid, invalid []string) {
	paths := filepath.SplitList(os.Getenv("SSL_CERT_DIR"))
	if file := os.Getenv("SSL_CERT_FILE"); file != "" {
		paths = append(paths, file)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if holdsCertificate(p) {
			valid = append(valid, p)
		} else {
			invalid = append(invalid, p)
		}
	}
	return valid, invalid
}

// holdsCertificate reports whether the file, or any file in the directory,
// contains a PEM certificate.
func holdsCertificate(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	files := []string{path}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return false
		}
		files = files[:0]
		for _, e := range entries {
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err == nil && x509.NewCertPool().AppendCertsFromPEM(data) {
			return true
		}
	}
	return false
}

// UpstreamTLSMessages are logged when the attack starts: that skipping
// verification is unsafe, or which CAs verify the dependencies, so a forwarded
// request answered with a 502 is not a surprise.
func UpstreamTLSMessages(opts proxyfault.Opts, validCAs, invalidCAs []string) action_kit_api.Messages {
	if opts.TLSInterceptCA == nil {
		return nil
	}
	if opts.UpstreamInsecureSkipVerify {
		return action_kit_api.Messages{{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: "Unsafe, test environments only: intercepted HTTPS requests are forwarded to the dependency without verifying its certificate, so whoever answers at its address receives them, including any credentials, and their responses are trusted.",
		}}
	}
	var msgs action_kit_api.Messages
	for _, p := range invalidCAs {
		msgs = append(msgs, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("The custom CA path %s (SSL_CERT_DIR / SSL_CERT_FILE) is missing or holds no PEM certificate, so it can't verify dependencies.", p),
		})
	}
	if len(validCAs) == 0 {
		msgs = append(msgs, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: "Only public CAs verify the dependencies' certificates: no custom CA is configured (SSL_CERT_DIR / SSL_CERT_FILE). Forwarded HTTPS requests to a dependency with a private-CA certificate get a 502; configure its CA, or turn on Skip Dependency Certificate Verification.",
		})
	}
	return msgs
}

// UpstreamErrorTip explains failed forwards once they happen. They may be
// failed connections, but with HTTPS decrypted they may as well be a
// dependency certificate the proxy can't verify.
func UpstreamErrorTip(opts proxyfault.Opts, validCAs []string, upstreamErrors int64) *action_kit_api.Message {
	if upstreamErrors == 0 || !VerifiesUpstream(opts) {
		return nil
	}
	var tip string
	if len(validCAs) == 0 {
		tip = "Only public CAs verify the dependencies' certificates, so a dependency with a private-CA certificate fails"
	} else {
		tip = fmt.Sprintf("Custom CAs are configured (%s), but they may not include the CA of the dependency's certificate", strings.Join(validCAs, ", "))
	}
	return &action_kit_api.Message{
		Level: extutil.Ptr(action_kit_api.Warn),
		Message: fmt.Sprintf("The proxy failed to forward %d time(s): it couldn't connect, or couldn't verify the dependency's certificate. %s: configure its CA (SSL_CERT_DIR / SSL_CERT_FILE), or turn on Skip Dependency Certificate Verification.",
			upstreamErrors, tip),
	}
}
