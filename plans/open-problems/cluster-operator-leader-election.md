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

## A refused `Lease` turns the election off

[unelected.go](../../cluster-operator/unelected.go) serves a rollback
onto a release from before the election. That release's RBAC grants
no write of the `Lease`, so a copy that waited for the `Lease` would
never act, and the rollback would stop. The copy acts without an
election when the API server answers a `Lease` request with
`403 Forbidden`, or answers the `Lease`'s create with `404 NotFound`.

The code cannot tell that case from any other refusal. Every 403 on
any `Lease` request turns the mode on, including a 403 that does not
come from a missing grant. Two cases follow from one such answer:

* **A waiting copy.** With `replicas: 2`, the copy that does not lead
  reads the `Lease` every 5 to 11 seconds. One 403 on that read makes
  it act beside the leader until its next read, 5 to 11 seconds later.
  In that time both copies sweep, and each can grant a reboot turn
  from its own view of the fleet. That is the budget overrun that the
  election exists to prevent.
* **The leader.** A 403 on a renewal turns the mode on while the copy
  leads. `mayWrite` in [leader.go](../../cluster-operator/leader.go)
  then allows every write and does not check the age of the last
  renewal, so the guard's limit, one renewal deadline after the last
  renewal, does not apply. A renewal that succeeds turns the mode off
  again. If none does, the elector stops the lead and the process
  exits by 25 seconds after the last renewal, the same as when the
  guard applies. The margin that the guard gives against a paused
  leader is lost for that time.

No log from a fleet shows this happen. The window follows from the
code.

### What would close it

1. **Require a refusal that lasts.** Act without an election only
   after the `Lease` answers 403, or 404 on create, on every try for
   one `Lease` duration, 30 seconds. A single stray 403 then changes
   nothing. A rollback onto the older RBAC waits 30 seconds longer
   for the cluster operator's writes. For the leader, keep the
   renewal-age check in `mayWrite` while the mode is on, so a refused
   renewal cannot extend the lead. This changes only the cluster
   operator.
2. **Remove the mode.** When no release that a `Cluster` can roll back
   to is older than the election, every copy runs under RBAC that
   grants the `Lease`, and a refusal is an ordinary failure of the
   election. Deleting `unelected.go` then closes the window
   completely. This needs a rule for which releases a rollback may
   target, which does not exist yet.

The recommendation is option 1 now, and option 2 when the release
window allows it. A `SelfSubjectAccessReview` that asks whether the
grant exists does not close the window: the same authorizer answers
it, so an authorizer that refused the `Lease` in error can refuse the
review in the same way.

## A Lease that names a process that has ended

When the only API server of a fleet reboots, the leader cannot renew
its `Lease`. It exits by 25 seconds after its last renewal, and the
kubelet starts its container again. The new process has a new
identity. client-go measures a `Lease`'s duration from the time a copy
first reads the current record, not from `renewTime`, because the
holder can run on another node with another clock. So each copy that
reads the ended process's `Lease` waits a whole duration, 30 seconds,
from that first read, and the fleet has no acting copy in that time.

`renewalClock.Get` and `clearIfHeld` in
[leader.go](../../cluster-operator/leader.go) close this case for the
same pod. A process can take or clear a `Lease` that an earlier
process of its own pod held, once the last renewal is one duration
old. The kubelet starts a container's process again only after the
one before it ended, and that process wrote `renewTime` from the same
node's clock.

Two cases stay open:

* **Another pod.** A copy in another pod cannot tell an ended holder
  from a paused one. On a test cluster with one server, the server's
  reboot applied a new cluster operator manifest, and the rolling
  update stopped the restarted process. The new pod waited 32 seconds
  for the `Lease`, and the next machine's reboot turn started 34
  seconds late. With the change above, the restarted process takes the
  `Lease` or clears it before it stops, if the API server answers it
  first. The wait stays when the stop comes before that answer. It
  happens at most once for each reboot of the only server.
* **The other operators.** `media-operator`, `library-operator`, and
  `equipment-operator` each carry a copy of this election, with the
  same scheme of one new identity for each process, and none has this
  change. A copy must also keep the pod's name as its hostname, because
  the identity starts with the hostname.

### What would close it

1. **Copy the change** to the three operators' `leader.go`. It needs
   no design choice.
2. **Read the holder's pod.** The identity starts with the pod's name.
   A copy could read that pod, and treat a pod that does not exist as
   ended. A pod that is terminating can still run its process, so only
   a missing pod is proof, and the change needs a read of pods in the
   cluster operator's RBAC. The gain is at most one `Lease` duration
   for each reboot of the only server.

The recommendation is option 1 now. Option 2 is not worth its RBAC
for a fleet with more than one server, where the API server stays up.

Not known: whether a fleet with three servers loses the `Lease` when
the server that a copy's connection uses reboots. The in-cluster
address reaches every server, but a request in flight to the server
that stops fails, and a renewal deadline of 10 seconds allows only one
retry.

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
