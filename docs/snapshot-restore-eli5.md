# Network attacks that don't break the host on cleanup — ELI5

## The problem

On a regular Linux box, the network card has a small set of rules attached to it that decide *how* packets leave: a thing called a **qdisc tree**. Cloud providers (GKE, AKS, EKS) tune this tree at boot for high throughput — bigger buffers, lower latencies, a specific layout.

When you run a Steadybit network attack (delay, packet loss, corruption, bandwidth limit, …), the agent has to **temporarily replace** that tree with its own that knows how to drop or delay packets. When the attack ends, the agent removes its tree.

The catch: when the agent removes its tree, the kernel automatically puts a **default** tree back. That default tree is *not* the same as the one the cloud provider had tuned. It has smaller buffers, different timings, a different layout. Same shape, wrong settings.

**Customer-visible symptom**: a chaos experiment ran, finished, and now their host's network is degraded until the node reboots.

---

## Why the kernel doesn't just "remember"

When you do `tc qdisc del root` (the agent's cleanup command), the kernel literally throws the tree away. There's no "undo" in the kernel. The next time anything needs a network policy, the kernel says "no tree here, let me attach the default one" — and that default is whatever the kernel was compiled with, not whatever was running before.

The cloud provider's tuning was applied at boot by a script. The kernel doesn't know that. So after `tc del`, the tuning is gone.

```
┌────────────────────┐         ┌──────────────────────┐         ┌────────────────────┐
│   Before attack    │         │    During attack     │         │    After cleanup   │
│  (tuned by GKE)    │   →     │  (agent installed    │   →     │  (kernel default,  │
│                    │         │   its own tree)      │         │   NOT the tuned    │
│  mq 8026:          │         │  prio 1:             │         │   one)             │
│  ├ fq buckets=     │         │  └ netem corrupt 15% │         │  mq 0:             │
│  │  32768          │         │                      │         │  ├ fq buckets=1024 │
│  ├ fq horizon=2s   │         │                      │         │  ├ fq horizon=10s  │
│  └ ...             │         │                      │         │  └ ...             │
└────────────────────┘         └──────────────────────┘         └────────────────────┘
                                                                          ↑
                                                                  ❌ host slow until
                                                                     reboot
```

---

## The fix in one sentence

**Take a photo of the tree before the attack starts. Put it back after the attack ends.**

The "photo" is taken via Linux's RTNETLINK API. It captures every qdisc on every relevant interface, including their parameters (bucket counts, horizons, rate limits, …). When the attack cleans up, we replay the photo onto the kernel — same handles, same parameters, byte-identical.

```
┌─────────┐ ┌──────────┐ ┌─────────┐ ┌──────────┐ ┌──────────┐
│ 1.      │ │ 2.       │ │ 3.      │ │ 4.       │ │ 5.       │
│ Snapshot│ │ Install  │ │ Attack  │ │ Remove   │ │ Replay   │
│ the     │→│ attack   │→│ runs    │→│ attack   │→│ snapshot │
│ tree    │ │ tree     │ │         │ │ tree     │ │          │
└─────────┘ └──────────┘ └─────────┘ └──────────┘ └──────────┘
   📸                                                  ↑
                                              ✅ host back to
                                                 exactly how it was
```

A few details that made this harder than it sounds:

- The kernel auto-recreates the **root** qdisc itself (the `mq` parent) when its children get attached, but it does so with a hidden handle that userspace can't reference. So we explicitly **claim** the original handle back via a raw RTNETLINK message — that's the line of work that took the longest.
- The `Get` API returns each qdisc with some kernel-only counter fields populated. The same library refuses to `Replace` an object that has those fields set. So we **strip** them on the local copy before replay.
- Children's `Parent` references the parent's handle. After the kernel re-attaches its anonymous root, those references no longer resolve. We rewrite them to point at the new live root before replay.

---

## Why it's behind a flag

This whole path touches Linux's network-stack APIs directly — including some that aren't well-documented and behave differently between distros and kernel versions. We tested it end-to-end on GKE Standard (Container-Optimized OS, kernel 6.12) with the customer's exact `mq + fq buckets=32768 horizon=2s` profile, and the diff between *before* and *after* is empty.

But we want one round of real customer validation before flipping it on by default. Hence the opt-in flag.

```yaml
extension-container:
  image:
    tag: main
  extraEnv:
    - name: STEADYBIT_EXTENSION_NETWORK_STRICT_ROOT_QDISC
      value: "false"
extension-host:
  image:
    tag: main
  extraEnv:
    - name: STEADYBIT_EXTENSION_NETWORK_STRICT_ROOT_QDISC
      value: "false"
```

Notice it's a **single flag** with **two states**:

- `true` (default): network attacks **refuse to run** on managed-cloud roots (instead of degrading them). Safe-by-default.
- `false`: attacks run, **and** snapshot/restore preserves the original tree.

There's deliberately no separate "snapshot on/off" knob — the two behaviours are the same intent ("be careful with the host") expressed differently.

---

## How an operator confirms it worked

In the agent logs, after enabling the flag, two new lines appear around each network attack lifecycle:

**At attack start:**
```
INF captured qdisc snapshot
    interfaces=1
    snapshot="qdisc mq 8026:0 dev eth0 root
              qdisc fq 802b:0 dev eth0 parent 8026:1 buckets 32768 horizon 2s
              qdisc fq 8029:0 dev eth0 parent 8026:2 buckets 32768 horizon 2s"
```

**At attack end:**
```
INF qdisc restore verified: post-restore state matches snapshot
    restored_state="<identical to the snapshot above>"
```

The "verified" suffix means the agent re-read the tree after restoring and confirmed the kernel accepted the replay. If anything diverges, the log line becomes a `WARN` with a diff of what was lost.

---

## Affected attacks

Any of the six tc-based network attacks benefit from this on a tuned host:

- Network Delay
- Network Package Loss
- Network Package Corruption ← *the one the customer reported*
- Network Limit Bandwidth
- Network Blackhole
- TCP Reset

DNS-based attacks (DNS Error Injection, Block DNS) use a separate mechanism (iptables + a userspace responder) and were already safe — nothing in their cleanup touches the qdisc tree.

---

## What's next

After we get a couple of customer confirmations that the flag works on their setup, we plan to:

1. Flip the default so the snapshot/restore path is on out of the box.
2. Remove the flag entirely (eventually) — at that point the agent always preserves the host's network configuration, no operator action needed.
