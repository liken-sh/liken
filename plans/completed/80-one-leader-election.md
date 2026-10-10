# 80, One leader election for every singleton operator

Proposed, built, and drilled on liken-1 on 2026-10-10, except for
the cluster operator, which no drill has run yet (see "The drill on
liken-1"). The work moves the leader election into a new
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

## The drill on liken-1

Run on 2026-10-10 on liken-1, a cluster of two machines, `liken-1`
and `stick-1`, on `liken` 2026.10.10-001. Five operators ran the
development build `2026.10.10-001-dev-022-04d9d691`, and every pod
of the drill ran on the machine `liken-1`. The times come from the
container log's timestamps and from the `Lease`, read every half
second.

The cluster operator was not drilled. The OS image carries its binary,
and the OS publishes no development builds, so liken-1 runs the cluster
operator of 2026.10.10-001, with its own copy of the election. The
drill of the cluster operator waits for the next release of `liken`.

### The handover in a rollout

`kubectl rollout restart` on each `Deployment`. The handover is the
time from the old process's release, the write of the `Lease` with no
holder and a duration of 1 second, to the new process's `holding
Lease` line. client-go's release succeeded each time, so no old
process logged `released Lease`: only the second release in
`Lead.end` logs that line, and it found nothing to clear.

| Operator | Strategy | Release | Holding | Handover |
|---|---|---|---|---|
| `media-operator` | `RollingUpdate` | 18:07:39.664 | 18:07:44.773 | 5.1 s |
| `library-operator` | `RollingUpdate` | 18:08:07.398 | 18:08:18.305 | 10.9 s |
| `observatory-operator` | `RollingUpdate` | 18:08:26.519 | 18:08:33.660 | 7.1 s |
| `people-operator` | `RollingUpdate` | 18:08:42.532 | 18:08:49.271 | 6.7 s |
| `equipment-operator` | `Recreate` | 18:12:29.926 | 18:12:31.122 | 1.2 s |

Each handover is within the 11 seconds that a waiting copy takes to
read the `Lease` again. With `RollingUpdate`, each new process read
the `Lease` before the old one released it, logged `waiting for
Lease`, and took it on its next read. In `observatory-operator` and
`people-operator`, the new process logged `waiting for Lease` 70 and
53 milliseconds before the release, and logged nothing else until
`holding Lease`, so one copy acted at a time. With `Recreate`, the old
pod had stopped before the new process started, so the new process
took the `Lease` on its first read. The gap with no holder was 1.2
seconds.

### The restart in place

A `kubectl debug node/liken-1 --profile=sysadmin` pod sent `SIGKILL`
to the leader's process, and the kubelet started the container again
in the same pod at once. The take is the time from the last renewal
to the new process's `holding Lease` line.

| Operator | Last renewal | First read | Holding | From the renewal | From the first read |
|---|---|---|---|---|---|
| `media-operator` | 18:09:19.904 | 18:09:20.755 | 18:09:53.932 | 34.0 s | 33.2 s |
| `library-operator` | 18:10:03.468 | 18:10:08.064 | 18:10:34.688 | 31.2 s | 26.6 s |
| `observatory-operator` | 18:10:33.861 | 18:10:37.227 | 18:11:11.666 | 37.8 s | 34.4 s |
| `people-operator` | 18:11:09.493 | 18:11:14.482 | 18:11:41.766 | 32.3 s | 27.3 s |
| `equipment-operator` | 18:11:43.199 | 18:11:43.670 | 18:12:18.009 | 34.8 s | 34.3 s |

Every take came 31 to 38 seconds after the last renewal: the `Lease`
expired at 30 seconds, and the next read came up to 8 seconds later.
`library-operator` and `people-operator` took the `Lease` 26.6 and
27.3 seconds after their first read, which client-go alone does not
do, because it waits a whole duration from that read. So the take of
an expired `Lease` of the same pod (`renewalClock.Get`) ran. In the
other three, the first read came less than 4 seconds after the last
renewal, and the two rules give times too close to tell apart.
