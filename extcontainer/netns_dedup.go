// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"strconv"
	"sync"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
)

// netnsAttackTracker coordinates network attacks between multiple containers
// that share a network namespace. In Kubernetes this is the default for
// every pod: all containers in the pod share the pod's netns so they see
// each other on localhost. Steadybit fires an apply per container target;
// the platform doesn't know they share a netns, so extension-container ends
// up running the SAME tc-attack twice on the SAME netns. The second apply
// then collides at the kernel level (e.g. htb.change rejects because the
// first apply already installed active classes at handle 1:).
//
// The tracker gives us a per-netns claim: the first Start on a netns is
// "primary" and applies the attack; subsequent Starts on the same netns are
// "shadow" and no-op. Stop mirrors that decision — the primary reverts,
// shadows no-op — using the IsShadow flag persisted on NetworkActionState.
//
// The in-memory counter here only coordinates concurrent Start calls within
// a single extension-container process. IsShadow lives on state, so Stop
// still does the right thing after an extension pod restart.
//
// Restart caveat: if the extension pod restarts *between* the primary's
// Start (already applied) and a shadow's Start (hasn't run yet), the
// tracker resets to empty; the shadow's Start then thinks it's the primary
// and tries to apply, which will collide with the primary's still-installed
// tc rules. This is a rare race — usually all Starts for one experiment
// arrive within the same second — and the collision surfaces as a normal
// Apply error the platform can retry. Persisting the tracker to disk to
// close the race isn't worth the complexity today.
var netnsAttackTracker = struct {
	sync.Mutex
	active map[string]int
}{active: map[string]int{}}

// netNsID returns the tracker key for a target process's network namespace.
// Matches the format used by netfault's runcRunner.id() so both layers key
// by the same identifier — inode preferred (immutable and unique per
// namespace instance on a single kernel), path as fallback for cases where
// the caller didn't populate the inode.
func netNsID(process ociruntime.LinuxProcessInfo) string {
	for _, ns := range process.Namespaces {
		if ns.Type == specs.NetworkNamespace {
			if ns.Inode != 0 {
				return strconv.FormatUint(ns.Inode, 10)
			}
			return ns.Path
		}
	}
	return ""
}

// claimNetnsForAttack increments the netns counter and reports whether this
// caller is the first (should apply the attack). Later callers on the same
// netns receive false (should skip apply and mark themselves as shadow).
// Empty id short-circuits to true so an unknown-netns target still applies
// — safer than silently no-op'ing.
func claimNetnsForAttack(id string) bool {
	if id == "" {
		return true
	}
	netnsAttackTracker.Lock()
	defer netnsAttackTracker.Unlock()
	first := netnsAttackTracker.active[id] == 0
	netnsAttackTracker.active[id]++
	return first
}

// releaseNetnsForAttack decrements the netns counter after a Stop. The
// return value isn't used by the current call sites (Stop consults
// NetworkActionState.IsShadow to decide whether to revert), but keeping it
// symmetric with claim makes future refcount-based decisions straightforward.
// Idempotent when the tracker doesn't know about this netns (e.g. after a
// restart wiped the in-memory state).
func releaseNetnsForAttack(id string) {
	if id == "" {
		return
	}
	netnsAttackTracker.Lock()
	defer netnsAttackTracker.Unlock()
	n := netnsAttackTracker.active[id]
	if n <= 1 {
		delete(netnsAttackTracker.active, id)
		return
	}
	netnsAttackTracker.active[id] = n - 1
}
