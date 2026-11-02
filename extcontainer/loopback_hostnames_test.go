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
	}{
		{hostname: "localhost", want: true},
		{hostname: "Api.LOCALHOST.", want: true},
		{hostname: "127.0.0.1", want: true},
		{hostname: "10.0.0.1"},
		{hostname: "toys", want: true},
		{hostname: "TOYS.internal", want: true},
		// The selector matches subdomains, so a subdomain entry counts.
		{hostname: "shop.test", want: true},
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
			assert.Equal(t, tc.want, loopbackHostname(tc.hostname, etcHosts))
		})
	}
}

func Test_loopbackHostname_noEtcHosts(t *testing.T) {
	assert.False(t, loopbackHostname("toys", nil))
	assert.True(t, loopbackHostname("localhost", nil))
}
