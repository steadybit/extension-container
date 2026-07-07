// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"bytes"
	"encoding/json"
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
// The tracker gives us three outcomes per Start:
//
//   - **Primary**: first Start to arrive on this netns. Applies the attack.
//   - **Shadow**: a later Start arrives on a netns whose primary has the
//     IDENTICAL opts. Skips apply, no-ops on stop. Ref-counted so Stop
//     properly cleans up the claim.
//   - **Passthrough**: a later Start arrives on a netns whose primary has
//     DIFFERENT opts (e.g. different attack type, or delay vs blackhole).
//     Doesn't touch the tracker at all — the request flows through to
//     netfault, whose `doesConflictWith`/`pushActiveNetfault` layer then
//     decides whether to accept or reject with a visible error. This
//     preserves the pre-PR user-visible-error behavior for combined
//     attacks on the same pod, which the netns-only variant would have
//     silently no-op'd.
//
// The primary/shadow decision lives on NetworkActionState (`IsShadow`,
// `NetnsClaimed`), so Stop still routes correctly after an extension pod
// restart between Start and Stop.
//
// Restart caveat: if the extension pod restarts *between* the primary's
// Start (already applied) and a shadow's Start (hasn't run yet), the
// in-memory tracker resets to empty; the shadow's Start then thinks it's
// the primary and tries to apply, which will collide with the primary's
// still-installed tc rules. Rare in practice — Start calls usually arrive
// within the same second — and the collision surfaces as a normal Apply
// error the platform can retry. Persisting the tracker to disk to close
// the race isn't worth the complexity today.
var netnsAttackTracker = struct {
	sync.Mutex
	active map[string]*netnsEntry
}{active: map[string]*netnsEntry{}}

type netnsEntry struct {
	// count is the number of live primary+shadow claims. Passthroughs are
	// not counted (they don't touch the tracker).
	count int
	// opts is the serialized attack opts of the primary. Later Starts on
	// this netns compare their opts against this: identical -> shadow,
	// different -> passthrough. json.RawMessage bytes come from the same
	// json.Marshal in the same process across sibling containers, so byte
	// equality is a reliable "same attack" check for the dedup case.
	opts json.RawMessage
}

// ClaimResult is what claimNetnsForAttack tells Start to do.
type ClaimResult int

const (
	// ClaimPrimary is the first (or freshest, after full release) claim on
	// this netns. Start applies the attack. Stop must release the counter
	// exactly once via NetnsClaimed=true on state.
	ClaimPrimary ClaimResult = iota
	// ClaimShadow means a sibling container already claimed this netns
	// with IDENTICAL opts. Start skips apply. Stop skips revert. Release
	// counter exactly once via NetnsClaimed=true.
	ClaimShadow
	// ClaimPassthrough means a sibling claimed this netns with DIFFERENT
	// opts. Start proceeds normally (Apply is called). Netfault then decides
	// whether to accept as compatible or reject as conflicting. Stop reverts
	// normally. Passthrough does NOT touch the counter — NetnsClaimed=false
	// on state so Stop won't try to release.
	ClaimPassthrough
)

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

// claimNetnsForAttack inspects the tracker for the given netns and returns
// what the caller's Start should do. See the doc on ClaimResult for the
// three outcomes. `opts` is the serialized attack opts (state.NetworkOpts)
// — the same JSON bytes any sibling container would produce for the same
// attack in the same process.
//
// Empty id short-circuits to ClaimPrimary so an unknown-netns target still
// applies — safer than silently no-op'ing.
func claimNetnsForAttack(id string, opts json.RawMessage) ClaimResult {
	if id == "" {
		return ClaimPrimary
	}
	netnsAttackTracker.Lock()
	defer netnsAttackTracker.Unlock()
	entry, exists := netnsAttackTracker.active[id]
	if !exists {
		netnsAttackTracker.active[id] = &netnsEntry{count: 1, opts: opts}
		return ClaimPrimary
	}
	if bytes.Equal(entry.opts, opts) {
		entry.count++
		return ClaimShadow
	}
	// Different opts — let it through to netfault, don't touch the
	// tracker. Netfault's own doesConflictWith / pushActiveNetfault will
	// either allow both or reject the newcomer with a visible error.
	return ClaimPassthrough
}

// releaseNetnsForAttack decrements the netns counter after a Stop. Only
// call when the corresponding Start returned ClaimPrimary or ClaimShadow
// (checked at the call site via state.NetnsClaimed). Idempotent when the
// tracker doesn't know about this netns (e.g. after a restart wiped the
// in-memory state).
func releaseNetnsForAttack(id string) {
	if id == "" {
		return
	}
	netnsAttackTracker.Lock()
	defer netnsAttackTracker.Unlock()
	entry, exists := netnsAttackTracker.active[id]
	if !exists {
		return
	}
	if entry.count <= 1 {
		delete(netnsAttackTracker.active, id)
		return
	}
	entry.count--
}
