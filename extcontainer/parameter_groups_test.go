// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"testing"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/stretchr/testify/assert"
)

func groupMembers(desc action_kit_api.ActionDescription) map[string][]string {
	members := map[string][]string{}
	for _, p := range desc.Parameters {
		if p.Group != nil {
			members[*p.Group] = append(members[*p.Group], p.Name)
		}
	}
	return members
}

func definedGroups(desc action_kit_api.ActionDescription) []string {
	var names []string
	if desc.ParameterGroups != nil {
		for _, g := range *desc.ParameterGroups {
			names = append(names, g.Name)
		}
	}
	return names
}

func TestParameterGroups(t *testing.T) {
	trafficFilter := []string{"hostname", "ip", "port", "excludeHostname", "excludeIp", "networkInterface"}
	dependencyFilter := []string{"ip", "port", "excludeIp", "attackLocalhost"}

	tests := []struct {
		name    string
		desc    action_kit_api.ActionDescription
		groups  map[string][]string
		defined []string
	}{
		{"blackhole", NewNetworkBlackholeContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: {"hostname", "ip", "port", "excludeHostname", "excludeIp", "blockLocalhost"}}, []string{groupTrafficFilter}},
		{"delay", NewNetworkDelayContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: append(append([]string{}, trafficFilter...), "tcpDataPacketsOnly")}, []string{groupTrafficFilter}},
		{"loss", NewNetworkPackageLossContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: trafficFilter}, []string{groupTrafficFilter}},
		{"corrupt", NewNetworkCorruptPackagesContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: trafficFilter}, []string{groupTrafficFilter}},
		{"bandwidth", NewNetworkLimitBandwidthContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: trafficFilter}, []string{groupTrafficFilter}},
		{"tcpReset", NewNetworkTcpResetContainerAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: {"ip", "port", "excludeHostname", "excludeIp", "networkInterface", "attackLocalhost"}}, []string{groupTrafficFilter}},
		{"dependencyDelay", NewNetworkDelayDependencyAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: dependencyFilter}, []string{groupTrafficFilter}},
		{"dependencyHttpAbort", NewNetworkHttpAbortDependencyAction(nil, nil).Describe(),
			map[string][]string{groupTrafficFilter: dependencyFilter}, []string{groupTrafficFilter}},
		{"fillDisk", NewFillDiskContainerAction(nil, nil).Describe(),
			map[string][]string{groupFillBehavior: {"method", "blocksize", "failOnOomKill"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.groups, groupMembers(tt.desc))
			assert.Equal(t, tt.defined, definedGroups(tt.desc))

			for _, p := range tt.desc.Parameters {
				advanced := p.Advanced != nil && *p.Advanced
				// Platforms without groups keep these in "Additional Settings", so a grouped parameter stays advanced
				// and no advanced parameter is left alone in "Additional Settings" on platforms with groups.
				assert.Equal(t, advanced, p.Group != nil, "parameter %s", p.Name)
			}
		})
	}
}
