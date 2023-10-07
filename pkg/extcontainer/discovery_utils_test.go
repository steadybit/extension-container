package extcontainer

import (
	"context"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func Test_schedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wantedTargets = []discovery_kit_api.Target{{
		Id: "id",
	}}

	decorated := schedule(ctx, 1*time.Second, func(_ context.Context) ([]discovery_kit_api.Target, error) {
		return wantedTargets, nil
	})

	assert.Eventually(t, func() bool {
		targets, _ := decorated(ctx)
		return assert.Equal(t, wantedTargets, targets)
	}, 3*time.Second, 1*time.Second)
}
