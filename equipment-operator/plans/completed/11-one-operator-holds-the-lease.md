# One operator holds the `Lease`

Plan 11. Built on 2026-09-27, and tested on the laptop against fake API
servers. No drill has run on a cluster yet, and the drill that is owed
is at the end of this plan. Plan 10 moved the watches to client-go and
left leader election open, with two facts that stopped it. This plan
answers both and builds it. It also closes three findings of plan 10's
reviews: the `Deployment` held each `Receiver` twice, the node
workload's watches cover the whole cluster, and the operator's own HTTP
client had no deadline on a whole request.

## The problem

The `Deployment`'s operator is a cluster singleton. It holds one session
on each receiver, creates and deletes the `Receiver` and `Television`
objects that discovery owns, and writes their status. Two copies that
act at once race: both open a session on one receiver and send it
commands, or both create one `Television`. The `Deployment` runs one
replica with the `Recreate` strategy, but a node partition or a patch
of the replica count can still run a second copy.

Plan 10 named two facts that stopped the organization's design, a
`Lease` through client-go's `tools/leaderelection` and a
`RollingUpdate`:

1. The same binary runs as the `cec` `DaemonSet`, and
   `tools/leaderelection` links the typed clientset.
2. The `Deployment` uses the host network and port 9260, so a surge pod
   cannot start on the node that runs the old one.

## The design

### Two builds in one image

The image holds two builds of the program, the way media-operator's
does. `/equipment-operator` is the full build, and the `Deployment`
runs it. `/equipment-operator-node` is built with the tag `node`, and
`deploy/cec.yaml` runs it as `/equipment-operator-node cec`.

`leader.go` holds the election and carries `//go:build !node`.
`leader_node.go` replaces it in the node build: its `lead` refuses to
run the `Deployment`'s operator and exits. The node build links no
leader election and no typed clientset. Its watches link only
client-go's `tools/cache`, `dynamic`, and `rest`.

In the node build, `lead` exits and `actWhileLeading` never calls its
argument, so the linker leaves out the `Deployment`'s code as well:
`serve`, the `Receiver` loop, and discovery are not in the node binary.

`make test-go` runs `go vet -tags node .`, and fails when
`go list -tags node -deps .` names `k8s.io/client-go/kubernetes`,
`informers`, `dynamic/dynamicinformer`, or `tools/leaderelection`. CI
runs `make test-go`, and the release builds both binaries from the
`Dockerfile`.

### The election

`leader.go` follows media-operator's `leader.go`. The `Lease` is
`equipment-operator` in the pod's namespace, a `resourcelock.LeaseLock`
with `ReleaseOnCancel: true`. The identity is the pod's name and a
random suffix, so a restarted container is a new candidate. The pod
states `POD_NAME` and `POD_NAMESPACE`, and the operator exits when
either is unset.

The timings are a lease duration of 30 seconds, a renewal deadline of
10 seconds, and a retry period of 5 seconds. A leader that stops
renewing gives up at the renewal deadline, and client-go's release can
take up to another renewal deadline before the process exits, so the
leader exits by 25 seconds after its last renewal. With a 15-second
duration, a new leader could start while the old one can still write.
The comment at `operatorLeaseTiming` gives the arithmetic.

`actWhileLeading` waits for the `Lease` and then runs
`operateAsLeader`, which binds the metrics listener and runs `serve`.
So a copy that waits binds no port, searches for no device, opens no
receiver session, and sends the API server nothing but its reads of
the `Lease`, and the create of the `Lease` when none exists. A
`SIGTERM` while the copy waits ends the wait, and the process exits
with nothing done.

On a `SIGTERM` while it leads, `serve` returns after every part that
writes has stopped. The `CECBus` loop and the watches end, and `serve`
joins them. The `Receiver` loop stops every unit, and then waits for
each goroutine that a unit or a session starts and that can write: the
driver, the status writer, the unit's and the session's bus
connections, the settings and command handlers, and the one-shots of a
session. It also waits for discovery, which creates and deletes
`Receiver` objects. Each such goroutine starts through `goWork`
(`work.go`), which counts it in a group that the loop's context
carries. Then `end` releases the `Lease`.

The wait is bounded by `workStopWait`, 5 seconds. Each goroutine ends
on its context, and a request of the operator's own client ends at its
30-second deadline at the latest. When the group does not empty in
time, `serve` answers `errStillWriting`, and the operator does not
release the `Lease`: the process exits holding it, and a waiting copy
takes it when it expires, 30 seconds after the last renewal.

A loss of the `Lease` exits the process with code 1 at once, because a
session in flight must not keep writing after another copy takes the
`Lease`.

client-go's release can fail on a normal shutdown. Cancelling the
election does not recall a renewal already sent, so that renewal can
land after the release read the `Lease`, and the release then fails on
a conflict. A waiting copy would then wait the whole lease duration.
`end` waits for the election to end, reads the `Lease` again, and when
it still names this process, writes it with no holder, a duration of
one second, and the current renewal time. A conflict reads it again, up
to three times, within half the renewal deadline. This is
media-operator's fix, and the same deterministic test proves it: it
fails 20 times in 20 runs with `clearIfHeld` removed, and passes 200
times in 200 runs under `-race`.

The election is not fencing. A leader that pauses, for example on a
stalled node, can resume after its `Lease` expired and finish a request
it had already sent.

### The strategy stays `Recreate`

A `RollingUpdate` starts the new pod before it stops the old one. The
pod uses the host network and declares port 9260, so the new pod needs
a second node, and on a one-node cluster it stays `Pending` and the
rollout never finishes. So the strategy stays `Recreate`, and the
`Lease` guards against a second copy that a partition or a replica
patch starts.

`replicas: 2` is safe, and the manifest says so. The API server gives
a host-network pod a `hostPort` equal to each `containerPort` it
declares. A read of the host-network pods on liken-1 showed it:
`bluetooth-operator` declares 9250 and carries `hostPort: 9250`, and
`liken-machine-operator` declares 9200 and carries `hostPort: 9200`.
The scheduler places no two pods with the same `hostPort` on one node,
so the second copy runs on another node and waits there, or stays
`Pending` on a one-node cluster.

### RBAC

A `Role` in the operator's namespace grants `get` and `update` on the
one `Lease` name, and `create` on `leases`, because RBAC cannot read a
name off a create. A `RoleBinding` gives it to the operator's
`ServiceAccount`. The node workload's identity gets nothing new.

### One `Receiver` watch in the `Deployment`

The `Deployment` ran two informers on the `Receiver` objects, one for
the `Receiver` loop and one for the `CECBus` loop's `Television` pass,
so it held each `Receiver` twice. `watchReceivers` is now the one
watch. Its handler wakes the `Receiver` loop on every change, and wakes
the `CECBus` loop only for a change that `watchReceiverSpecs` would
report. Its store is the one the `Television` pass reads. `serve` sets
`sharedReceivers` on the `CECBus` loop, so that loop starts no watch of
its own. A `CECBus` loop that runs alone, as in its tests, still
watches the `Receiver` specs itself.

### The node workload's watches stay cluster-wide

The node workload watches every `CECBus` and every `Display` in the
cluster, so each adapter node's heartbeat wakes the pass on every other
adapter node. A selector could scope the watches only if each object
carried a label or a selectable field that names the node, and neither
kind does:

* A `CECBus` names its machines in `spec.adapters`, a list. A field
  selector on a custom resource needs a `selectableFields` entry, which
  takes a scalar field, and the `CECBus` carries no label per machine.
* A `Display` names its node in `status.node`, a scalar. The `Display`
  definition belongs to display-operator, declares no
  `selectableFields`, and puts no node label on a `Display`.

So the watches stay as they are. A `selectableFields` entry for
`.status.node` in display-operator's definition would let the node
workload select its own `Display` objects. That change belongs to
display-operator.

### A deadline on a whole request

The operator's own `Client` sends every write and every read of a kind
that the reader writes, and none of its requests streams. Its HTTP
client now has `Timeout: apiRequestTimeout`, 30 seconds, from the dial
to the last byte of the body, so an API server that stops part way
through a body cannot hold a pass. The dial and header timeouts stay.

## Measurements

Both builds used the `Dockerfile`'s flags,
`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`, with Go 1.27.1,
the toolchain on the laptop. The `Dockerfile` builds with Go 1.27.0, so
the image's binaries can differ by a few kilobytes. Both columns come
from the same toolchain.
The image is `FROM scratch`, so its size is the sum of its binaries.

| | Before this plan (5dc329f) | After |
|---|---|---|
| The `Deployment`'s binary | 15,618,208 bytes, 397 packages | 29,135,008 bytes, 730 packages |
| The node workload's binary | the same binary | 13,992,096 bytes, 397 packages |
| The image | 15,618,208 bytes | 43,127,104 bytes |

A node with a CEC adapter pulls the whole image, about 43 MB where one
binary was about 16 MB. The node workload's own program is smaller than
before, because the linker leaves out the `Deployment`'s code. The RSS
is not measured yet; it is part of the drill below.

## Tests

| What | Test |
|---|---|
| The operator acts only while it holds the `Lease`, and releases it after it stops | `TestTheLeaseIsReleasedOnlyAfterTheOperatorStops` |
| A renewal that lands after client-go's release read the `Lease` leaves it released | `TestAStepDownReleasesTheLeaseAfterALateRenewal` |
| A waiting copy takes a released `Lease` well inside the lease duration | `TestAWaitingCopyTakesAReleasedLeaseAtOnce` |
| A leader that cannot renew exits, and so does one whose `Lease` another process took | `TestALeaderThatCannotRenewExits`, `TestALeaderWhoseLeaseAnotherProcessTookExits` |
| A stop while waiting runs nothing and leaves the holder alone | `TestAStopWhileWaitingLeavesTheHolderAlone` |
| At a shutdown, the loop waits for a settings write in flight before it returns | `TestTheLoopWaitsForAWriteInFlightBeforeItReturns` |
| A goroutine that does not stop in time is reported, so the `Lease` is kept | `TestAwaitWorkAnswersWhetherTheWorkStopped`, `TestAStillWritingOperatorKeepsTheLease` |
| A waiting copy binds no port, searches for no device, and sends the API server nothing but its reads of the `Lease`, and does all three once it leads | `TestAWaitingCopyDoesNothingUntilItLeads` |
| A steady leader renews with no read, and each process has its own identity | `TestASteadyLeaderRenewsWithNoRead`, `TestTheIdentityIsNewForEachProcess` |
| Through the reflector: the shared `Receiver` watch wakes the `CECBus` loop for a spec edit and not for a status write | `TestAReceiverEventWakesTheLoopThatReadsIt` |
| In the whole `Deployment`, a `Receiver` spec edit reaches its `Television` at once, and the `Receiver` objects have one watch | `TestTheDeploymentLoopFollowsAReceiversSpec`, `TestTheDeploymentWatchesTheReceiversOnce` |
| A body that stops part way ends the request at the deadline | `TestTheInClusterClientBoundsAWholeRequest` |

The leader tests run against the fake `Lease` server in
`leaseserver_test.go`, which is media-operator's, with durations of two
seconds so that a loss or a takeover happens in a few seconds.

## Considered and set aside

* **A second `main` package.** It keeps the two builds apart by
  package instead of by tag, and needs the code both workloads use
  moved into packages of their own. The root package holds both
  workloads, so the change is large.
* **A `RollingUpdate` with `maxSurge: 1`.** It needs a second node for
  the new pod, because of the host port, and never finishes on a
  one-node cluster.

## The drill still owed

On liken-1, with the operator on a build of this change:

1. Check that the `Deployment`'s pod logs `holding Lease
   liken-system/equipment-operator`, and that the `cec` pods run
   `/equipment-operator-node`.
2. Delete the operator's pod, and time the new pod's `holding` line
   from the old pod's `released Lease` line.
3. Scale the `Deployment` to two replicas, check that the second pod
   waits on another node and binds nothing, and scale it back to one.
4. Read the working set of the operator container and of one `cec`
   pod, and compare them with the build before this change.
