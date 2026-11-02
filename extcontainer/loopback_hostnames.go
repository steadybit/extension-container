// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/utils"
	"github.com/steadybit/extension-kit/extutil"
)

// loopbackHostnameMessages warns about dependency hostnames that most likely
// point at localhost, which the proxy doesn't intercept unless Attack Localhost
// Traffic is enabled — such a fault would silently never apply.
//
// It is an educated guess, not a resolution: it catches localhost names and
// literal 127.x addresses, plus names the target's /etc/hosts maps to 127.x
// only. DNS is not consulted. An unreadable /etc/hosts just skips that part.
func loopbackHostnameMessages(ctx context.Context, pid int, hostnames []string) action_kit_api.Messages {
	hosts, err := utils.RootCommandContext(ctx, "cat", filepath.Join("/proc", strconv.Itoa(pid), "root", "etc", "hosts")).Output()
	if err != nil {
		log.Debug().Err(err).Int("pid", pid).Msg("could not read the target's /etc/hosts; skipping the localhost hostname check")
	}

	var matched []string
	for _, hostname := range hostnames {
		if loopbackHostname(hostname, hosts) {
			matched = append(matched, hostname)
		}
	}
	if len(matched) == 0 {
		return nil
	}
	log.Warn().Strs("hostnames", matched).Msg("dependency hostnames seem to belong to localhost, which is not intercepted")
	return action_kit_api.Messages{{
		Level:   extutil.Ptr(action_kit_api.Warn),
		Message: fmt.Sprintf("Localhost traffic isn't intercepted. Enable 'Attack Localhost Traffic' to include %s.", strings.Join(matched, ", ")),
	}}
}

// loopbackHostname reports whether a dependency hostname seems to point at
// localhost.
func loopbackHostname(hostname string, etcHosts []byte) bool {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return true
	}
	if addr, err := netip.ParseAddr(name); err == nil {
		return addr.Unmap().IsLoopback()
	}

	// The selector also matches subdomains, so an /etc/hosts entry for
	// api.example.com counts for the hostname example.com.
	loopback, routable := false, false
	for entry, addrs := range parseEtcHosts(etcHosts) {
		if entry != name && !strings.HasSuffix(entry, "."+name) {
			continue
		}
		for _, addr := range addrs {
			switch {
			case addr.Is4() && addr.IsLoopback():
				loopback = true
			case addr.Is4():
				routable = true
			}
		}
	}
	return loopback && !routable
}

// parseEtcHosts returns every name in an /etc/hosts file with its addresses.
func parseEtcHosts(content []byte) map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}
		for _, name := range fields[1:] {
			name = strings.ToLower(strings.TrimSuffix(name, "."))
			out[name] = append(out[name], addr.Unmap())
		}
	}
	return out
}
