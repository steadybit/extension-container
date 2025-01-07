// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2025 Steadybit GmbH

package config

import (
	"github.com/gobwas/glob"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func Test_parseArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		config     Specification
		wantErr    bool
		wantConfig Specification
	}{
		{
			name: "Should not touch the existing config if no flags are set",
			config: Specification{
				DisallowHostNetwork:   true,
				DisallowK8sNamespaces: []DisallowedName{mustParseDisallowedName("test1"), mustParseDisallowedName("test2-*")},
			},
			wantConfig: Specification{
				DisallowHostNetwork:   true,
				DisallowK8sNamespaces: []DisallowedName{mustParseDisallowedName("test1"), mustParseDisallowedName("test2-*")},
			},
		},
		{
			name:   "Should enforce disallow and add missing namespaces",
			args:   []string{"-disallowHostNetwork", "-disallowK8sNamespaces=test1,test2-*"},
			config: Specification{},
			wantConfig: Specification{
				DisallowHostNetwork:   true,
				DisallowK8sNamespaces: []DisallowedName{mustParseDisallowedName("test1"), mustParseDisallowedName("test2-*")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldArgs := os.Args
			defer func() { os.Args = oldArgs }()
			os.Args = append([]string{"extension"}, tt.args...)

			if err := parseArgs(&tt.config); (err != nil) != tt.wantErr {
				t.Errorf("parseArgs() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				assert.Equal(t, tt.wantConfig, tt.config)
			}
		})
	}
}

func mustParseDisallowedName(s string) DisallowedName {
	return DisallowedName{
		g: glob.MustCompile(s),
	}
}
