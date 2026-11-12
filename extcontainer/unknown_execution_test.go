// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_status_of_an_unknown_execution_is_an_error(t *testing.T) {
	tests := map[string]func() (*action_kit_api.StatusResult, error){
		"fill disk":           statusOfUnknownExecution(NewFillDiskContainerAction(nil, nil), nil),
		"fill memory":         statusOfUnknownExecution(NewFillMemoryContainerAction(nil, nil), nil),
		"stress cpu":          statusOfUnknownExecution(NewStressCpuContainerAction(nil, nil), nil),
		"stop container":      statusOfUnknownExecution(NewStopContainerAction(nil), nil),
		"http delay":          statusOfUnknownExecution(NewNetworkDelayDependencyAction(nil, nil), nil),
		"dns error":           statusOfUnknownExecution(NewNetworkDNSErrorInjectionAction(nil, nil), nil),
		"tcp reset (L7 mode)": statusOfUnknownExecution(NewNetworkTcpResetContainerAction(nil, nil), func(s *NetworkTcpResetState) { s.L7 = true }),
	}
	for name, status := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := status()

			require.NoError(t, err)
			require.NotNil(t, result.Error)
			assert.Equal(t, action_kit_api.Errored, *result.Error.Status)
		})
	}
}

func Test_status_of_an_unknown_packet_level_tcp_reset_is_still_running(t *testing.T) {
	result, err := statusOfUnknownExecution(NewNetworkTcpResetContainerAction(nil, nil), nil)()

	require.NoError(t, err)
	assert.Nil(t, result.Error)
	assert.False(t, result.Completed)
}

func statusOfUnknownExecution[T any](action action_kit_sdk.Action[T], modify func(*T)) func() (*action_kit_api.StatusResult, error) {
	return func() (*action_kit_api.StatusResult, error) {
		state := action.NewEmptyState()
		if modify != nil {
			modify(&state)
		}
		return action.(action_kit_sdk.ActionWithStatus[T]).Status(context.Background(), &state)
	}
}
