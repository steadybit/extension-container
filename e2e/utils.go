package e2e

import (
	"context"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_test/e2e"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func assertFileHasSize(t *testing.T, m *e2e.Minikube, pod metav1.Object, containername string, filepath string, wantedSizeInMb int, wantedDeltaInMb int) {
	sizeInBytes := wantedSizeInMb * 1024 * 1024
	deltaInBytes := wantedDeltaInMb * 1024 * 1024
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	message := ""
	for {
		select {
		case <-ctx.Done():
			assert.Fail(t, "file has not the expected size", message)
			return

		case <-time.After(200 * time.Millisecond):
			out, err := m.PodExec(pod, containername, "stat", "-c", "%s", filepath)
			if err != nil {
				message = fmt.Sprintf("%s: %s", err.Error(), out)
				continue
			}

			if fileSize, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				actualDelta := int(math.Abs(float64(fileSize - sizeInBytes)))
				if actualDelta <= deltaInBytes {
					return
				} else {
					message = fmt.Sprintf("file size is %d, wanted %d, delta of %d exceeds allowed delta of %d", fileSize, sizeInBytes, actualDelta, deltaInBytes)
				}
			} else {
				message = fmt.Sprintf("cannot parse file size: %s", err.Error())
			}
		}
	}
}
