// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"errors"
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pauseMockClient struct {
	types.Client
	state      types.ContainerState
	stateErr   error
	unpaused   bool
	unpauseErr error
}

func (c *pauseMockClient) State(_ context.Context, _ string) (types.ContainerState, error) {
	return c.state, c.stateErr
}

func (c *pauseMockClient) Unpause(_ context.Context, _ string) error {
	c.unpaused = true
	return c.unpauseErr
}

func TestPauseStatus(t *testing.T) {
	tests := []struct {
		name          string
		state         types.ContainerState
		stateErr      error
		wantCompleted bool
		wantSummary   string
	}{
		{name: "keeps running while paused", state: types.StatePaused},
		{name: "completes when the container is gone", state: types.StateStopped, wantCompleted: true, wantSummary: "Container nginx is not running anymore, the pause ended early"},
		{name: "completes when the container isn't paused anymore", state: types.StateRunning, wantCompleted: true, wantSummary: "Container nginx is not paused anymore, the pause ended early"},
		{name: "keeps running when the state can't be read", state: types.StateStopped, stateErr: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := &pauseAction{client: &pauseMockClient{state: tt.state, stateErr: tt.stateErr}}

			result, err := action.Status(context.Background(), &PauseActionState{ContainerId: "docker://123", TargetLabel: "nginx"})

			require.NoError(t, err)
			assert.Equal(t, tt.wantCompleted, result.Completed)
			assert.Nil(t, result.Error)
			if tt.wantSummary != "" {
				require.NotNil(t, result.Summary)
				assert.Equal(t, action_kit_api.SummaryLevelWarning, result.Summary.Level)
				assert.Equal(t, tt.wantSummary, result.Summary.Text)
			} else {
				assert.Nil(t, result.Summary)
			}
		})
	}
}

func TestPauseStop(t *testing.T) {
	tests := []struct {
		name        string
		state       types.ContainerState
		stateErr    error
		wantUnpause bool
		wantMessage string
		wantSummary string
	}{
		{name: "unpauses a paused container", state: types.StatePaused, wantUnpause: true, wantMessage: "Unpaused container nginx"},
		{name: "skips a container which is gone", state: types.StateStopped, wantMessage: "Container nginx is not running anymore", wantSummary: "Container nginx is not running anymore, the pause ended early"},
		{name: "skips a container which isn't paused anymore", state: types.StateRunning, wantMessage: "Container nginx is not paused anymore", wantSummary: "Container nginx is not paused anymore, the pause ended early"},
		{name: "unpauses when the state can't be read", state: types.StateStopped, stateErr: errors.New("boom"), wantUnpause: true, wantMessage: "Unpaused container nginx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &pauseMockClient{state: tt.state, stateErr: tt.stateErr}
			action := &pauseAction{client: client}

			result, err := action.Stop(context.Background(), &PauseActionState{ContainerId: "docker://123", TargetLabel: "nginx"})

			require.NoError(t, err)
			assert.Equal(t, tt.wantUnpause, client.unpaused)
			require.NotNil(t, result.Messages)
			require.Len(t, *result.Messages, 1)
			assert.Equal(t, tt.wantMessage, (*result.Messages)[0].Message)
			if tt.wantSummary != "" {
				require.NotNil(t, result.Summary)
				assert.Equal(t, action_kit_api.SummaryLevelWarning, result.Summary.Level)
				assert.Equal(t, tt.wantSummary, result.Summary.Text)
			} else {
				assert.Nil(t, result.Summary)
			}
		})
	}
}
