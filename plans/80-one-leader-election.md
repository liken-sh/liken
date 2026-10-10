# 80, One leader election for every singleton operator

Proposed on 2026-10-10. The work moves the leader election into a new
package, `kubernetes/election`, and makes every singleton operator
elect through it: `liken`'s cluster operator, `media-operator`,
`library-operator`, `equipment-operator`, `observatory-operator`, and
`people-operator`.

## The problem

Four operators hold a copy of the same election, in four `leader.go`
files of 266 to 467 lines. The copies have drifted:

- Only the cluster operator takes the expired `Lease` of an earlier
  process of its own pod at once (`renewalClock.Get`). A leader that
  exits because it cannot renew, and that the kubelet restarts in the
  same pod, waits a whole `Lease` duration in the other three. That is
  30 seconds with no acting copy, each time the only API server
  reboots.
- Only the cluster operator has the write guard, which refuses a write
  once the last renewal is one renewal deadline old. In the other
  three, a leader that cannot renew keeps writing until the elector
  gives up, up to 15 seconds later.
- `observatory-operator` and `people-operator` hold no election at
  all. They run one replica with the `Recreate` strategy, which keeps
  two copies apart during a rollout and does not keep them apart during
  a node partition: the replacement pod starts while the old pod still
  runs on the node that the control plane cannot reach.

## The design

### One package

`kubernetes/election` holds the election that the cluster operator has
now, with every part of it:

- client-go's `leaderelection` with a `LeaseLock`, at the durations of
  30, 10, and 5 seconds that every copy already uses.
- An identity of the pod's name and a random suffix.
- The release on a shutdown, client-go's and then a second one that
  survives a late renewal.
- The exit on a lost `Lease`.
- The take of an expired `Lease` of an earlier process of the same pod.
- The write guard, as a function that an operator passes to
  `apiclient.Client.WithWriteGuard`.

An operator names the `Lease`, its namespace, and the pod, and calls
`Await` before it acts and `StepDown` after its last write. An
operator that cannot show its last write has ended does not call
`StepDown`: it exits holding the `Lease`, and a waiting copy takes it
when it expires.

The cluster operator's action without an election, while the API server
refuses the `Lease` (`unelected.go`), is an option of the package that
only the cluster operator turns on. Its reason is the cluster
operator's own: the OS image carries the binary, so a rollback can run
it under the RBAC of a release from before the election. Every other
operator's manifest carries its RBAC and its image together.

The package links client-go's typed clientset for the coordination
group. The pod build of `media-operator` and `library-operator` and the
node build of `equipment-operator` must not link it, and their
`Makefile`s check that. Only the files of the operator build import the
package.

`kubernetes/election/electiontest` holds the fake API server of the
`Lease`, so that each operator tests its own use of the election.

### Two more operators elect

`observatory-operator` and `people-operator` elect through the package,
and their `Deployment`s move to `RollingUpdate` with `maxSurge: 1` and
`maxUnavailable: 0`, as the `Deployment`s of the cluster operator,
`media-operator`, and `library-operator` have. Each `Deployment` gains
`POD_NAME` and `POD_NAMESPACE`, and each RBAC gains the `Role` of its
`Lease`.

`equipment-operator` keeps `Recreate`. Its pod uses the host network
and declares port 9260, so the scheduler places no two of its pods on
one node. A `RollingUpdate` on a one-node cluster leaves the new pod
`Pending`, and the rollout never finishes. Its election still guards
against a second copy that a node partition or a larger replica count
starts.

### What stays a plain singleton

`media-api`, `display-api`, `audio-api`, and the media bus stay one
replica with `Recreate`. Each one serves requests from state that
lives in its process, so a waiting copy has nothing to take over. The
git CSI driver's controller writes only a demand time on a
`PersistentVolume`, which two copies can write in any order, and its
two sidecars elect through their own upstream flags.

## Proof

- The package's tests: the cluster operator's tests of the election,
  in `synctest` bubbles against the fake `Lease` server.
- Each operator keeps a test of its own use: it waits before it acts,
  and it releases after its last write.
- On liken-1: restart each operator's container in place and time the
  next take, and roll each `Deployment` and time the handover.
