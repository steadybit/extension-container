// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import "github.com/steadybit/action-kit/go/action_kit_api/v2"

const (
	groupTrafficFilter = "Traffic Filter"
	groupFillBehavior  = "Fill Behavior"
)

var (
	trafficFilterGroup = action_kit_api.ParameterGroup{
		Name:        groupTrafficFilter,
		Description: "Leave unchanged to affect all traffic except localhost.",
	}
	dependencyTrafficFilterGroup = action_kit_api.ParameterGroup{
		Name:        groupTrafficFilter,
		Description: "Leave unchanged to intercept all connections to the dependency hostnames except localhost.",
	}
)
