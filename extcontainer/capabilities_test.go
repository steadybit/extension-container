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
	withMissingCapabilities(t, "NET_ADMIN", "BPF")

	err := requireCapabilities("Network attacks", networkCapabilities)

	var extErr extension_kit.ExtensionError
	require.ErrorAs(t, err, &extErr)
	assert.Equal(t, "Network attacks need the capabilities NET_ADMIN, which the extension does not have. "+
		"Add them to the capabilities of the extension's container securityContext.", extErr.Title)
	assert.NoError(t, requireCapabilities("Stress attacks", sidecarCapabilities), "stress does not need NET_ADMIN")
}

func TestNetworkPrepare_FailsFastWithoutNetAdmin(t *testing.T) {
	withMissingCapabilities(t, "NET_ADMIN")
	action := &networkAction{}

	_, err := action.Prepare(context.Background(), &NetworkActionState{}, action_kit_api.PrepareActionRequestBody{})

	assert.ErrorContains(t, err, "NET_ADMIN")
}
