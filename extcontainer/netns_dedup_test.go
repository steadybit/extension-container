// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/stretchr/testify/assert"
)

func fullyRelease(id string) {
	for {
		netnsAttackTracker.Lock()
		_, exists := netnsAttackTracker.active[id]
		netnsAttackTracker.Unlock()
		if !exists {
			return
		}
		releaseNetnsForAttack(id)
	}
}

// TestClaimAndRelease_PrimaryThenShadowThenReleaseCycle locks down the core
// dedup contract for the bug this fix addresses: first Start on a netns is
// primary, later Starts with IDENTICAL opts become shadows, and after
// everyone releases the netns is claimable again from scratch (so a fresh
// experiment reruns the primary/shadow logic correctly).
func TestClaimAndRelease_PrimaryThenShadowThenReleaseCycle(t *testing.T) {
	const id = "test-netns-1001"
	opts := json.RawMessage(`{"bandwidth":"1mbit","filter":{"include":["10.0.0.0/8"]}}`)
	t.Cleanup(func() { fullyRelease(id) })

	assert.Equal(t, ClaimPrimary, claimNetnsForAttack(id, opts), "first claim is primary")
	assert.Equal(t, ClaimShadow, claimNetnsForAttack(id, opts), "second identical claim is shadow")
	assert.Equal(t, ClaimShadow, claimNetnsForAttack(id, opts), "third identical claim is shadow")

	releaseNetnsForAttack(id)
	releaseNetnsForAttack(id)
	releaseNetnsForAttack(id)

	assert.Equal(t, ClaimPrimary, claimNetnsForAttack(id, opts), "fully released netns claims fresh as primary again")
}

// TestClaim_DifferentOptsPassThrough is the fix for Claude review finding
// #2: a sibling container running a DIFFERENT attack on the same netns
// must not be silently shadowed — it must pass through to netfault so the
// user-visible conflict error is preserved for combined attacks.
func TestClaim_DifferentOptsPassThrough(t *testing.T) {
	const id = "test-netns-different-opts"
	optsA := json.RawMessage(`{"bandwidth":"1mbit"}`)
	optsB := json.RawMessage(`{"corruption":10}`) // different attack shape entirely
	t.Cleanup(func() { fullyRelease(id) })

	assert.Equal(t, ClaimPrimary, claimNetnsForAttack(id, optsA))
	assert.Equal(t, ClaimPassthrough, claimNetnsForAttack(id, optsB), "different opts must pass through, not shadow")
	assert.Equal(t, ClaimShadow, claimNetnsForAttack(id, optsA), "identical opts still shadow even after a passthrough")
}

// TestClaim_PassthroughDoesNotTouchCounter guarantees passthroughs don't
// affect the ref-count. If a passthrough incremented it, a later
// Stop-of-primary wouldn't tear the counter back to zero and the netns
// would stay stuck in an "already claimed" state — exactly the leak
// pathology finding #1 flagged.
func TestClaim_PassthroughDoesNotTouchCounter(t *testing.T) {
	const id = "test-netns-passthrough-noop"
	optsA := json.RawMessage(`{"delay":100}`)
	optsB := json.RawMessage(`{"loss":50}`)
	t.Cleanup(func() { fullyRelease(id) })

	assert.Equal(t, ClaimPrimary, claimNetnsForAttack(id, optsA))
	assert.Equal(t, ClaimPassthrough, claimNetnsForAttack(id, optsB))
	assert.Equal(t, ClaimPassthrough, claimNetnsForAttack(id, optsB))

	// Only one release should be needed for primary — passthroughs don't
	// hold a counter. After it, the netns should be free.
	releaseNetnsForAttack(id)
	assert.Equal(t, ClaimPrimary, claimNetnsForAttack(id, optsB), "netns free again after single release; passthroughs didn't leak counter")
}

// TestClaim_DifferentNetnsAreIndependent verifies containers on different
// pods (different netns) never see each other — each is its own primary.
func TestClaim_DifferentNetnsAreIndependent(t *testing.T) {
	opts := json.RawMessage(`{}`)
	t.Cleanup(func() { fullyRelease("netns-a"); fullyRelease("netns-b") })

	assert.Equal(t, ClaimPrimary, claimNetnsForAttack("netns-a", opts))
	assert.Equal(t, ClaimPrimary, claimNetnsForAttack("netns-b", opts))
}

// TestClaim_EmptyIDShortCircuits guards against an unknown-netns target
// silently becoming a shadow. Empty ID means we couldn't identify the
// namespace at all; safer to apply than to no-op.
func TestClaim_EmptyIDShortCircuits(t *testing.T) {
	opts := json.RawMessage(`{}`)
	assert.Equal(t, ClaimPrimary, claimNetnsForAttack("", opts), "empty netns id always claims (safer than silent shadow)")
	assert.Equal(t, ClaimPrimary, claimNetnsForAttack("", opts), "second empty-id claim also primary — no shared state")
}

// TestClaim_ConcurrentIdenticalOptsExactlyOnePrimary exercises the mutex
// under contention: 32 goroutines racing on the same netns id with
// identical opts — exactly one gets ClaimPrimary, everyone else gets
// ClaimShadow. Regression guard for anyone refactoring the tracker without
// preserving atomicity.
func TestClaim_ConcurrentIdenticalOptsExactlyOnePrimary(t *testing.T) {
	const id = "test-netns-concurrent"
	const workers = 32
	opts := json.RawMessage(`{"bandwidth":"5mbit"}`)
	t.Cleanup(func() { fullyRelease(id) })

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		primaries int
		shadows   int
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			switch claimNetnsForAttack(id, opts) {
			case ClaimPrimary:
				mu.Lock()
				primaries++
				mu.Unlock()
			case ClaimShadow:
				mu.Lock()
				shadows++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, 1, primaries, "exactly one goroutine may claim the netns as primary")
	assert.Equal(t, workers-1, shadows, "every other goroutine sees identical opts and becomes shadow")
}

// TestRelease_IsIdempotentWhenTrackerForgot documents that releasing a
// netns the tracker doesn't know about (e.g. after an extension restart
// wiped the in-memory state) is a safe no-op — Stop can call this without
// having to check whether the tracker still remembers.
func TestRelease_IsIdempotentWhenTrackerForgot(t *testing.T) {
	assert.NotPanics(t, func() {
		releaseNetnsForAttack("test-netns-never-claimed")
		releaseNetnsForAttack("")
	})
}

// TestNetNsID_PrefersInode confirms netNsID uses the inode (immutable and
// unique per-namespace-instance) when available. This matches
// netfault.runcRunner.id() so both layers key by the same value.
func TestNetNsID_PrefersInode(t *testing.T) {
	p := ociruntime.LinuxProcessInfo{
		Namespaces: []ociruntime.LinuxNamespace{
			{Type: specs.MountNamespace, Path: "/proc/12345/ns/mnt", Inode: 4026531840},
			{Type: specs.NetworkNamespace, Path: "/proc/12345/ns/net", Inode: 4026531993},
		},
	}
	assert.Equal(t, "4026531993", netNsID(p), "inode wins over path")
}

// TestNetNsID_FallsBackToPath covers the case where Inode wasn't populated
// (rare, but LinuxProcessInfo permits it) — we still return a stable key
// so the tracker works.
func TestNetNsID_FallsBackToPath(t *testing.T) {
	p := ociruntime.LinuxProcessInfo{
		Namespaces: []ociruntime.LinuxNamespace{
			{Type: specs.NetworkNamespace, Path: "/proc/12345/ns/net"},
		},
	}
	assert.Equal(t, "/proc/12345/ns/net", netNsID(p))
}

// TestNetNsID_NoNetworkNamespaceReturnsEmpty documents that a target
// without a network namespace produces an empty id. Combined with the
// empty-id short-circuit in claimNetnsForAttack that keeps such targets
// from silently becoming shadows.
func TestNetNsID_NoNetworkNamespaceReturnsEmpty(t *testing.T) {
	p := ociruntime.LinuxProcessInfo{
		Namespaces: []ociruntime.LinuxNamespace{
			{Type: specs.MountNamespace, Path: "/proc/12345/ns/mnt", Inode: 42},
		},
	}
	assert.Empty(t, netNsID(p))
}
