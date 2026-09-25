// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"os"
	"testing"
)

// TestMain makes every capability available to the actions under test: the test process has none
// of the extension's capabilities, and the tests of other behaviour must not depend on the machine
// they run on. The capability tests stub their own scenarios.
func TestMain(m *testing.M) {
	missingCapabilities = func(...string) []string { return nil }
	os.Exit(m.Run())
}
