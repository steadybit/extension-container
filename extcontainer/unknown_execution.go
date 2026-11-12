// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
)

// unknownExecutionStatus is the status of an attack that is kept only in the
// memory of the extension instance that started it and is not known to this
// one. The agent never calls Status after Stop, and the SDK answers itself for
// executions it stopped, so this means the extension was restarted during the
// attack.
func unknownExecutionStatus() *action_kit_api.StatusResult {
	return &action_kit_api.StatusResult{
		Error: &action_kit_api.ActionKitError{
			Title:  "The extension lost track of the attack, most likely because it was restarted while the attack was running.",
			Status: extutil.Ptr(action_kit_api.Errored),
		},
	}
}
