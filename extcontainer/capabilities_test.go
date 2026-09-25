// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	extension_kit "github.com/steadybit/extension-kit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withMissingCapabilities(t *testing.T, missing ...string) {
	previous := missingCapabilities
	t.Cleanup(func() { missingCapabilities = previous })
	missingCapabilities = func(required ...string) []string {
		var out []string
		for _, r := range required {
			for _, m := range missing {
				if r == m {
					out = append(out, r)
				}
			}
		}
		return out
	}
}

func TestRequireCapabilities_NamesTheMissingOnes(t *testing.T) {
	withMissingCapabilities(t, "BPF", "SYS_TIME")

	err := requireCapabilities("DNS error injections", dnsInjectionCapabilities)

	var extErr extension_kit.ExtensionError
	require.ErrorAs(t, err, &extErr)
	assert.Equal(t, "DNS error injections need the capabilities BPF, which the extension does not have. "+
		"Add them to the capabilities of the extension's container securityContext.", extErr.Title)
	assert.NoError(t, requireCapabilities("Network attacks", networkCapabilities), "network faults do not need BPF")
	assert.NoError(t, requireCapabilities("Stress attacks", sidecarCapabilities))
}

func TestRequireCapabilities_EverySidecarNeedsNetAdmin(t *testing.T) {
	withMissingCapabilities(t, "NET_ADMIN")

	// The sidecar brings up the loopback interface of its network namespace.
	assert.ErrorContains(t, requireCapabilities("Stress attacks", sidecarCapabilities), "NET_ADMIN")
	assert.ErrorContains(t, requireCapabilities("Fill disk attacks", sidecarCapabilities), "NET_ADMIN")
}

func TestNetworkPrepare_FailsFastWithoutNetAdmin(t *testing.T) {
	withMissingCapabilities(t, "NET_ADMIN")
	action := &networkAction{}

	_, err := action.Prepare(context.Background(), &NetworkActionState{}, action_kit_api.PrepareActionRequestBody{})

	assert.ErrorContains(t, err, "NET_ADMIN")
}
