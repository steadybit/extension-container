// Copyright 2026 steadybit GmbH. All rights reserved.

package extcontainer

import (
	"sync"
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/stretchr/testify/assert"
)

// TestClaimAndRelease_FirstIsPrimary_SubsequentAreShadow locks down the core
// dedup contract: first Start on a netns applies, later Starts on the same
// netns become shadows, and after everyone releases the netns is claimable
// again from scratch (so a fresh attack cycle re-runs primary/shadow logic
// correctly).
func TestClaimAndRelease_FirstIsPrimary_SubsequentAreShadow(t *testing.T) {
	const id = "test-netns-1001"
	// Belt-and-braces: reset any state a parallel test may have leaked.
	t.Cleanup(func() { releaseNetnsForAttack(id); releaseNetnsForAttack(id) })

	assert.True(t, claimNetnsForAttack(id), "first claim is primary")
	assert.False(t, claimNetnsForAttack(id), "second claim on same netns is shadow")
	assert.False(t, claimNetnsForAttack(id), "third claim on same netns is shadow")

	releaseNetnsForAttack(id)
	releaseNetnsForAttack(id)
	releaseNetnsForAttack(id)

	assert.True(t, claimNetnsForAttack(id), "fully released netns claims fresh as primary again")
	t.Cleanup(func() { releaseNetnsForAttack(id) })
}

// TestClaim_DifferentNetnsAreIndependent verifies containers on different
// pods (different netns) never see each other — each is its own primary.
func TestClaim_DifferentNetnsAreIndependent(t *testing.T) {
	assert.True(t, claimNetnsForAttack("netns-a"))
	assert.True(t, claimNetnsForAttack("netns-b"))
	t.Cleanup(func() { releaseNetnsForAttack("netns-a"); releaseNetnsForAttack("netns-b") })
}

// TestClaim_EmptyIDShortCircuits guards against an unknown-netns target
// silently becoming a shadow. Empty ID means we couldn't identify the
// namespace at all; safer to apply than to no-op.
func TestClaim_EmptyIDShortCircuits(t *testing.T) {
	assert.True(t, claimNetnsForAttack(""), "empty netns id always claims (safer than silent shadow)")
	assert.True(t, claimNetnsForAttack(""), "second empty-id claim is also allowed — they're not shared state")
}

// TestClaim_ConcurrentReturnsExactlyOnePrimary exercises the mutex under
// contention: 32 goroutines racing on the same netns id — exactly one gets
// `true`, everyone else gets `false`. Regression guard for anyone
// refactoring the tracker without preserving atomicity.
func TestClaim_ConcurrentReturnsExactlyOnePrimary(t *testing.T) {
	const id = "test-netns-concurrent"
	const workers = 32
	t.Cleanup(func() {
		for i := 0; i < workers; i++ {
			releaseNetnsForAttack(id)
		}
	})

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		primaries int
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if claimNetnsForAttack(id) {
				mu.Lock()
				primaries++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, 1, primaries, "exactly one goroutine may claim the netns as primary")
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
