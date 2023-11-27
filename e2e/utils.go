package e2e

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
	"strings"
	"testing"
	"time"
)

func AssertFileHasSize(t *testing.T, m *e2e.Minikube, pod metav1.Object, containername string, filepath string, size int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	lastOutput := ""
	for {
		select {
		case <-ctx.Done():
			assert.Failf(t, "file not found", "file %s not found in container %s/%s.\n%s", filepath, pod.GetName(), containername, lastOutput)
			return

		case <-time.After(200 * time.Millisecond):
			var out string
			var err error
			out, err = m.PodExec(pod, containername, "wc", "-c", filepath)
			require.NoError(t, err, "failed to exec wc -c %s", filepath)

			for _, line := range strings.Split(out, " ") {
				if _, err := strconv.Atoi(line); err == nil {
					if line == fmt.Sprint(size) {
						return
					} else {
						log.Trace().Msgf("filesize is %s, expected %s", line, fmt.Sprint(size))
					}
				}
			}
			lastOutput = out
		}
	}
}