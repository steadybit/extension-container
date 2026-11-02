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
	"slices"
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

	var msgs action_kit_api.Messages
	for _, hostname := range hostnames {
		reason, ok := loopbackHostname(hostname, hosts)
		if !ok {
			continue
		}
		log.Warn().Str("hostname", hostname).Str("reason", reason).Msg("dependency hostname seems to belong to localhost, which is not intercepted")
		msgs = append(msgs, action_kit_api.Message{
			Level: extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("Hostname '%s' seems to belong to localhost (%s). Localhost traffic isn't intercepted, so it is probably not affected. Enable 'Attack Localhost Traffic' to include it.",
				hostname, reason),
		})
	}
	return msgs
}

// loopbackHostname reports whether a dependency hostname seems to point at
// localhost, and why.
func loopbackHostname(hostname string, etcHosts []byte) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return "a localhost name", true
	}
	if addr, err := netip.ParseAddr(name); err == nil {
		if addr.Unmap().IsLoopback() {
			return "a loopback address", true
		}
		return "", false
	}

	// The selector also matches subdomains, so an /etc/hosts entry for
	// api.example.com counts for the hostname example.com.
	var loopback []string
	routable := false
	for entry, addrs := range parseEtcHosts(etcHosts) {
		if entry != name && !strings.HasSuffix(entry, "."+name) {
			continue
		}
		for _, addr := range addrs {
			switch {
			case addr.Is4() && addr.IsLoopback():
				loopback = append(loopback, addr.String())
			case addr.Is4():
				routable = true
			}
		}
	}
	if len(loopback) == 0 || routable {
		return "", false
	}
	slices.Sort(loopback)
	return "mapped to " + strings.Join(slices.Compact(loopback), ", ") + " in the target's /etc/hosts", true
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
