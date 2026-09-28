# Coordinate cluster-operator instances

Open problem. The cluster operator elects one acting copy with a
`coordination.k8s.io` `Lease`
([milestone 71](../completed/71-the-operators-watch-through-client-go.md)).
The election does not fence a paused former leader, and a stale
reboot grant from that leader can exceed the disruption budget. This
document records that remaining concern and the options for it.

## What milestone 71 answered

`cluster-operator/leader.go` runs client-go's leader election on the
`liken-cluster-operator` `Lease` in `liken-system`. Only the copy that
holds it watches the fleet and writes. The `Deployment` rolls a new
pod in beside the old one, and `replicas: 2` is safe once every node
runs a release with the election.

The milestone answers these concerns of this problem:

* **Two instances.** A rolling update, a partitioned node, and a
  person who scales the `Deployment` can each run a second copy. Only
  the holder of the `Lease` acts.
* **Stop new work when leadership is lost.** A write guard on the
  operator's client refuses every write once the last renewal is one
  renewal deadline old, 10 seconds, and the elector exits the process
  by 25 seconds after the last renewal.
* **Bound requests.** The client abandons each request after 15
  seconds, so every write the guard allowed ends before a new leader
  can take the `Lease`, 30 seconds after the last renewal.
* **Stale writes.** The Lost verdict, the `Cluster`'s status, and a
  grant taken back each carry the `resourceVersion` of the object they
  change, so a stale copy conflicts or states what still holds. The
  milestone's plan has the table for every write the sweep makes.
* **A copy under an older release's RBAC.** A copy that the API
  server refuses on the `Lease` acts without an election, as the
  releases before the election did, until the `Lease` answers. Two
  copies can overlap for one retry period in that mode.
* **Partial grant sequences.** `carryOutRollout` writes the grants of
  one decision one `Machine` at a time. A crash between two writes
  leaves a subset of a decision that was inside the budget, and the
  next leader counts each written grant as a slot in flight.

## What stays open

`decideRollout` computes a fleet-wide decision, and `carryOutRollout`
in [rollout.go](../../cluster-operator/rollout.go) writes each grant
with the `resourceVersion` of that one `Machine`. A leader that pauses
between the write guard's check and the send, or with a grant in
flight, can land the grant after a new leader decided from a view of
the fleet without it. The new leader can grant another machine in the
same window, and the two grants together can exceed the budget, or put
two leaders down at once. Per-object version checks do not make the
budget a transaction.

## Options

The fence is not built. The options go to a design decision.

1. **A grant ledger in the `Cluster`'s status.** The sweep writes the
   set of machines that hold or receive a turn into the `Cluster`'s
   status first, with the `resourceVersion` of the `Cluster` it read,
   and writes the `Machine` grants only after that write succeeds. The
   next leader counts every machine in the ledger as a slot in flight.
   Two leaders cannot both commit a decision from the same `Cluster`
   version, so the budget becomes one conditional write. It adds a
   status field and changes only the cluster operator.
2. **A fencing token in each grant.** The grant names the `Lease`'s
   holder and its transition count, and the machine operator checks
   the current `Lease` before it drains. A stale grant is ignored. It
   changes the grant's shape and both operators, and a machine operator
   from an older release ignores the token.
3. **Narrow the window only.** One grant for each sweep, with a direct
   read of every `Machine` just before it. This needs no API change,
   and it is still not a fence.

The recommendation is option 1, the grant ledger: it is a true
compare-and-swap on one object, and the machine operator does not
change.

## Related problem

The separate `media-operator` repository records the same
single-instance concern in
`plans/open-problems/two-operators-can-run-at-once.md`, and elects its
leader with the same design and the same release fix.

## Verification needed

Run two copies against the same API server. Pause the active copy
beyond the `Lease`'s duration, let the other copy take the lead, then
resume the old one. Check that its writes are refused and that it
exits, and that grants stay within the budget and the one-leader floor
while a grant is in flight during the pause.
