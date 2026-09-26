# Coordinate cluster-operator instances

Open problem. The cluster operator is deployed as one replica, but the
code has no leader election. More than one instance can issue rollout
grants and update fleet state. The replica setting does not show that
concurrent decisions from two instances are safe.

## How two instances can run at once

The `Deployment` uses `replicas: 1` and `strategy: Recreate`. This
setting orders ordinary template replacements. It does not stop a second
process from running.

A partitioned node can continue running an old pod while Kubernetes
creates a replacement elsewhere. A partition does not always cause a
replacement: that depends on the observed failure and on controller
behavior. An operator or automation can also patch `replicas` to `2`,
which deliberately creates two instances. `Recreate` does not prevent
either case.

## What the instances write

The cluster operator updates `Cluster` and `Machine` status, issues
`RebootApproved` grants, and evicts stale system pods. `init` on leaders
writes the OS `AddOn` manifests, and `k3s` applies them. The cluster
operator's reconcile loop does not write them.

A comment in [main.go](../../cluster-operator/main.go) says that overlap
is safe, because both instances derive their decisions from cluster
state and use optimistic concurrency. Optimistic concurrency protects
each resource update from a conflicting version. It does not make two
instances read the same fleet snapshot, and it does not hold a budget
across different resources.

`decideRollout` computes a fleet-wide decision, then `carryOutRollout`
in [rollout.go](../../cluster-operator/rollout.go) writes grants one
`Machine` at a time. The open concern is conflicting decisions from
different snapshots. A partial write followed by a crash also needs
safe recovery, though a partial write alone does not show that the
budget was exceeded.
This review did not reproduce a violation with two instances.

## Proposed safeguard

Use a named `coordination.k8s.io` `Lease` to elect the active instance.
Only that instance would run mutating reconciliation. Acquisition,
renewal, and stopping work after renewal failure should use a tested
leader-election protocol.

The machine heartbeat code already uses `Lease` objects, but heartbeat
renewal is not an election algorithm. The election needs ownership
checks, expiry handling, and safe handoff. A contender should acquire an
expired lease through a conditional update, without a person
transferring it.

A `Lease` does not block writes to other API resources. A paused former
leader can resume after another instance has acquired the lease.
Requests already in flight can complete after local cancellation. The
implementation must bound requests and stop new work when leadership is
lost. It must also show how stale writes and partial grant sequences
stay safe. Per-object version checks help, but they do not make the
rollout budget a transaction.

## Remedy scope

The fix is implementation reliability work, plus a concurrency design
that needs verification. The intended design already has one active
fleet coordinator. Leader election can be added without a change to the
`Cluster` API, the disruption budget, or the normal single-replica
deployment. The work is more than a heartbeat: the handoff and the
write-safety protocol need review and failure tests.

Support for multiple standby replicas as an HA feature is a separate
operational decision. Takeover time would include lease expiry, retry,
and scheduling delays, so one lease duration does not bound the downtime.

## Related problem

The separate `media-operator` repository records the same single-instance
concern in `plans/open-problems/two-operators-can-run-at-once.md`.
Both operators need coordination in code; a replica count does not give
it. Compare their election and write-safety requirements before they
share an implementation.

## Verification needed

Run two instances against the same API and vary their snapshots, response
delays, and write order. Pause the active instance beyond lease expiry,
allow a replacement to acquire leadership, then resume the old instance.
Verify that grants stay within budget and leader disruption constraints.
Also test crashes between grant writes and leadership loss while a
request is in flight.
