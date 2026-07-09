// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"bytes"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
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
	// opts is the NORMALIZED attack opts of the primary — the raw
	// state.NetworkOpts run through normalizeOptsForDedup, which strips
	// the per-target TargetExecutionId nonce. Later Starts compare their
	// own normalized opts against this: identical -> shadow, different
	// -> passthrough.
	//
	// The stripping is essential: extension-container's mapToExecutionContext
	// sets TargetExecutionId = request.ExecutionId, and Steadybit fires a
	// separate ExecutionId per container target. Two sibling containers
	// of the same pod running the same experiment therefore have DIFFERENT
	// state.NetworkOpts bytes, and a naive byte comparison would always
	// disagree, forcing every sibling into the passthrough branch and
	// reproducing the "Change operation not supported" collision this
	// tracker exists to prevent.
	//
	// Note ExperimentExecutionId is deliberately KEPT in the comparison:
	// per action_kit_api it's experiment-scoped (shared by every sibling
	// target within one experiment run), so leaving it in correctly
	// scopes dedup to "same experiment, sibling container" and prevents
	// two unrelated experiments on the same pod from being folded into a
	// single Primary/Shadow pair — where the primary's Stop would tear
	// down the other experiment's still-active attack.
	opts []byte
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
// three outcomes. `opts` is state.NetworkOpts — the serialized attack
// config — which we normalize (strip per-execution nonces) before
// comparison so sibling containers with different ExecutionIds still
// resolve to the same identity.
//
// Empty id short-circuits to ClaimPrimary so an unknown-netns target still
// applies — safer than silently no-op'ing.
func claimNetnsForAttack(id string, opts json.RawMessage) ClaimResult {
	if id == "" {
		return ClaimPrimary
	}
	normalized := normalizeOptsForDedup(opts)
	netnsAttackTracker.Lock()
	defer netnsAttackTracker.Unlock()
	entry, exists := netnsAttackTracker.active[id]
	if !exists {
		netnsAttackTracker.active[id] = &netnsEntry{count: 1, opts: normalized}
		return ClaimPrimary
	}
	if bytes.Equal(entry.opts, normalized) {
		entry.count++
		return ClaimShadow
	}
	// Different opts — let it through to netfault, don't touch the
	// tracker. Netfault's own doesConflictWith / pushActiveNetfault will
	// either allow both or reject the newcomer with a visible error.
	return ClaimPassthrough
}

// normalizeOptsForDedup strips the per-target TargetExecutionId nonce from
// serialized attack opts so two sibling containers of the same pod running
// the same experiment produce identical output. Without this the tracker
// would never match siblings, because extension-container's
// mapToExecutionContext injects request.ExecutionId (unique per action
// target) into the opts as TargetExecutionId — and Steadybit fires one
// action target per container.
//
// ExperimentExecutionId is deliberately NOT stripped: per action_kit_api
// it's the ExecutionContext.ExecutionId, shared by every sibling target
// within one experiment run, so leaving it in the comparison scopes
// dedup correctly to "same experiment, sibling container." Stripping it
// would collapse two unrelated experiments running the same attack on
// the same pod into a single Primary/Shadow pair, letting whichever
// experiment finishes first tear down the other's still-active attack.
//
// Unmarshaling to a map and remarshaling also normalizes key order to
// alphabetical, so incidental field-order drift wouldn't cause a
// false-negative comparison. Marshal-error fallback returns the raw
// bytes: comparison still works for byte-identical inputs, just won't
// dedup across sibling ExecutionIds — closer to pre-fix behavior than
// crashing.
func normalizeOptsForDedup(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Warn().Err(err).Msg("dedup: failed to parse opts for normalization; falling back to raw byte compare")
		return raw
	}
	// LimitBandwidthOpts (and siblings) embed ExecutionContext, so its
	// fields appear at the top level of the serialized JSON.
	delete(m, "TargetExecutionId")
	out, err := json.Marshal(m)
	if err != nil {
		log.Warn().Err(err).Msg("dedup: failed to re-marshal normalized opts; falling back to raw byte compare")
		return raw
	}
	return out
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
