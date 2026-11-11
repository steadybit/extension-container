// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Runs without minikube: go test ./e2e -run Test_parseCurlWriteOut
func Test_parseCurlWriteOut(t *testing.T) {
	// PodExec's TTY ends lines with "\r\n"; one connection carried all four
	// requests, two of them faulted.
	got := parseCurlWriteOut("503 1\r\n200 0\r\n503 0\r\n200 0\r\n")
	require.Equal(t, keepAliveResult{responses: 4, faulted: 2, connects: 1}, got)

	// Plain "\n", and noise that is not a write-out line, work too.
	got = parseCurlWriteOut("200 1\n\ncurl: (28) timed out\n503 1\n")
	require.Equal(t, keepAliveResult{responses: 2, faulted: 1, connects: 2}, got)

	// A reset request gets no response: 000, and curl opens a new connection
	// for the next one.
	got = parseCurlWriteOut("200 1\r\n000 1\r\n200 1\r\n")
	require.Equal(t, keepAliveResult{responses: 3, reset: 1, connects: 3}, got)
}
