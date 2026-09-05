# Coordinate cluster-operator instances

Open problem. The cluster operator is deployed as one replica, but the
code has no leader election. More than one instance can issue rollout
grants and update fleet state. The safety of those concurrent decisions
has not been established by the replica setting.

## How overlap can occur

The `Deployment` uses `replicas: 1` and `strategy: Recreate`. This
sequences ordinary template replacements; it is not a process-level
exclusion mechanism.

A partitioned node can continue running an old pod while Kubernetes
creates a replacement elsewhere. Whether a replacement starts depends
on the observed failure and controller behavior, not merely on the
existence of a partition. An operator or automation can also patch
`replicas` to `2`, deliberately creating two instances. `Recreate`
does not prevent either case.

## What the instances write

The cluster operator updates `Cluster` and `Machine` status, issues
`RebootApproved` grants, and evicts stale system pods. OS `AddOn`
manifests are written by `init` on leaders and applied by `k3s`; they
are not an output of the cluster operator's reconcile loop.

[main.go](../../cluster-operator/main.go) argues that overlap is safe
because both instances derive their decisions from cluster state and
use optimistic concurrency. That protects individual resource updates
from conflicting versions. It does not prove that two instances read
the same fleet snapshot or preserve a budget across different resources.

`decideRollout` computes a fleet-wide decision, then `carryOutRollout`
in [rollout.go](../../cluster-operator/rollout.go) writes grants one
`Machine` at a time. The open concern is conflicting decisions from
different snapshots. A partial write followed by a crash also needs
safe recovery, though a partial write alone does not prove a budget
violation. No two-instance violation was reproduced in this review.

## Proposed safeguard

Use a named `coordination.k8s.io` `Lease` to elect the active instance.
Only that instance would run mutating reconciliation. Acquisition,
renewal, and stopping work after renewal failure should use a tested
leader-election protocol.

The machine heartbeat code already uses `Lease` objects, but heartbeat
renewal is not an election algorithm. The election needs ownership
checks, expiry handling, and safe handoff. A contender should acquire an
expired lease through a conditional update, not require a person to
transfer it.

A `Lease` is also not a fence on writes to other API resources. A paused
former leader can resume after another instance has acquired the lease;
requests already in flight can complete after local cancellation. The
implementation must bound requests, stop new work when leadership is
lost, and establish how stale writes and partial grant sequences remain
safe. Existing per-object version checks are useful but do not themselves
make the rollout budget a transaction.

## Remedy scope

**Implementation reliability work with a concurrency design to verify.**
The intended contract already has one active fleet coordinator. Adding
leader election need not change the `Cluster` API, disruption budget, or
normal single-replica deployment. It is more than adding a heartbeat:
the handoff and write-safety protocol need review and failure tests.

Supporting multiple standby replicas as an HA feature is a separate
operational decision. Takeover latency would include lease expiry,
retry, and scheduling delays; one lease duration is not a guaranteed
downtime bound.

## Related problem

The separate `media-operator` repository records the same singleton
concern in `plans/open-problems/two-operators-can-run-at-once.md`.
Both operators need code-level coordination rather than reliance on a
replica count alone. Their election and write-safety requirements should
be compared before sharing an implementation.

## Verification needed

Run two instances against the same API and vary their snapshots, response
delays, and write order. Pause the active instance beyond lease expiry,
allow a replacement to acquire leadership, then resume the old instance.
Verify that grants stay within budget and leader disruption constraints.
Also test crashes between grant writes and leadership loss while a
request is in flight.
