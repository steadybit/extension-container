// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_loopbackHostname(t *testing.T) {
	etcHosts := []byte(`# Kubernetes-managed hosts file.
127.0.0.1	localhost
::1	localhost ip6-localhost
10.0.0.7	my-pod
127.0.0.1	toys toys.internal # local forward proxy
127.0.1.1	api.shop.test
::1	v6only
127.0.0.2	both
10.0.0.8	both
`)
	cases := []struct {
		hostname string
		want     bool
		reason   string
	}{
		{hostname: "localhost", want: true, reason: "a localhost name"},
		{hostname: "Api.LOCALHOST.", want: true, reason: "a localhost name"},
		{hostname: "127.0.0.1", want: true, reason: "a loopback address"},
		{hostname: "10.0.0.1"},
		{hostname: "toys", want: true, reason: "mapped to 127.0.0.1 in the target's /etc/hosts"},
		{hostname: "TOYS.internal", want: true, reason: "mapped to 127.0.0.1 in the target's /etc/hosts"},
		// The selector matches subdomains, so a subdomain entry counts.
		{hostname: "shop.test", want: true, reason: "mapped to 127.0.1.1 in the target's /etc/hosts"},
		// Also routable: some connections can still be faulted.
		{hostname: "both"},
		// IPv6 loopback is never intercepted anyway.
		{hostname: "v6only"},
		{hostname: "my-pod"},
		{hostname: "api.example.com"},
		// Only a subdomain of the selector counts, not a mere suffix.
		{hostname: "oys"},
	}
	for _, tc := range cases {
		t.Run(tc.hostname, func(t *testing.T) {
			reason, ok := loopbackHostname(tc.hostname, etcHosts)
			assert.Equal(t, tc.want, ok)
			assert.Equal(t, tc.reason, reason)
		})
	}
}

func Test_loopbackHostname_noEtcHosts(t *testing.T) {
	_, ok := loopbackHostname("toys", nil)
	assert.False(t, ok)
	_, ok = loopbackHostname("localhost", nil)
	assert.True(t, ok)
}
