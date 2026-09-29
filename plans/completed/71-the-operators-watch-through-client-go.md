# 71. The operators watch through client-go, and the cluster operator elects a leader

Milestone 71. Built 2026-09-27. No drill has run yet. The numbers
below come from the tests' fake API servers and from a harness on a
laptop, and the drill that is still owed is at the end of this plan.

The machine operator and the cluster operator now watch the API server
through client-go's reflector, and each reconcile pass reads the
watches' in-memory copies instead of sending a request for each object
it judges. A settled machine sends one request every ten seconds in
place of eight, and a settled sweep of the fleet sends one in place of
ten. The cluster operator elects one acting copy with client-go's
leader election on a `Lease`, so its `Deployment` rolls a new pod in
beside the old one.

The open problem
[Coordinate cluster-operator instances](../../liken/plans/open-problems/cluster-operator-leader-election.md)
stays open. This milestone answers every concern it raises except
one: a paused former leader can still write a reboot grant from a
stale view of the fleet. That needs a fence, and the fence needs a
design decision (see "What is still open").

## The problem

**One hand-written watch loop.** `kubernetes/watch.go` held liken's
watch loop, `WatchMachines`, with 450 lines of tests for the loop
alone. On 2026-09-27 the organization chose client-go's reflector for
every operator, because upstream maintains and tests it (the
`operators` skill in `.agents` records the decision and the lean form).
`bluetooth-operator` plan 09 is the reference port.

**A pass read every object on a timer.** The machine operator's pass
runs at least every ten seconds, because the ticker is the heartbeat's
clock and the backstop for the kernel and file state that sends no
event. The operator watched only its own `Machine`. Every pass read the
rest from the API server:

| Request | When |
|---|---|
| `GET` its `Machine` | every ticker pass, in the loop |
| `LIST` its own operator pod | every pass |
| `GET` its `Node` | every pass |
| `GET` its `ResourceSlice` | every pass |
| `GET` the `Cluster` | every pass |
| `GET` the registry credentials `Secret` | every pass |
| `GET` its heartbeat `Lease` | every pass |
| `PUT` its heartbeat `Lease` | every ticker pass |

That is eight requests for each ticker pass, about 2,900 requests an
hour from each machine, and all of them except the heartbeat's `PUT`
found nothing new on a settled machine.

The cluster operator's sweep read about ten collections and objects
on each pass, and every status write in the fleet woke a full sweep.

**Two copies of the cluster operator could act at once.** The
`Deployment` ran one replica with `strategy: Recreate`, and the code
had no leader election. A rolling update, a partitioned node, and a
person who scales the `Deployment` each run a second copy, and two
copies can grant reboot turns from two views of the fleet. The open
problem records the concerns in full.

## The design

### The watches: `kubernetes/informer`

The new package `kubernetes/informer` runs one client-go informer for
each watched collection, in the lean form the organization chose: it
imports `tools/cache`, `dynamic`, and `rest`, and never `informers`,
`dynamicinformer`, or `kubernetes`. It is a package of its own because
the `liken` CLI imports the `kubernetes` package and watches nothing,
so client-go stays out of the CLI. `init` and the log relays do not
import it either.

It follows the reference port: `cache.NewInformerWithOptions` with
`&unstructured.Unstructured{}`, a transform that removes
`metadata.managedFields`, `runtime.DefaultUnstructuredConverter` into
the operator's own structs, a log line for an object that does not
convert, and a wake on `HasSyncedChecker().Done()` after the first
read. It adds four things that the operators here need:

* **A copy the pass reads.** `informer.Get`, `List`, and `ByIndex`
  answer from the informer's store. Each answers `ok` false while the
  copy cannot answer, and the caller then reads the API server, the
  way the pass did before. `LabelIndex` indexes a copy by one label.
* **A copy answers only while its watch is accepted.** A copy that has
  synced but whose last watch the API server refused is not current.
  This happens in a release skew: a new binary under the previous
  release's RBAC, which grants `list` and not `watch`. The reflector
  then lists the collection again after each backoff, up to thirty
  seconds apart, and a pass that read that copy could judge a live
  machine by a heartbeat thirty seconds old. So `Synced` is true only
  while the last watch the reflector opened was accepted.
* **A copy does not answer before it holds this process's own
  write.** A copy lags the API server by the few milliseconds the
  watch takes to deliver a change. A pass that runs right after its
  own write can read the copy before the watch delivered the write.
  For the cluster operator that is unsafe: a sweep that does not see
  the grant it just wrote counts one fewer machine in flight, and a
  change another machine wrote before the grant can already be in the
  copy, so the sweep can grant a second turn beyond the budget. So
  each status write records the `resourceVersion` it returned
  (`Collection.Wrote`), and the copy does not answer for that object,
  or answer a list, until the watch delivers that version or a later
  one. The pass
  reads the API server in the meantime. A write whose outcome is
  unknown, such as a timeout, settles at the object's next change or
  at the next direct read (`Observed`). `PublishStatus` and
  `PublishClusterStatus` now answer the written version. Kubernetes'
  own controllers call this mechanism expectations.
* **Two wake rules.** `WakeOnChange` wakes the loop on every change to
  an object, and ignores an update whose `resourceVersion` did not
  move. `WakeOnEdit` wakes the loop only when `metadata.generation`,
  `metadata.uid`, or the deletion mark changed, so a status write does
  not wake it. Both convert the object first and log one that does
  not convert.

`liken_watch_restarts_total{kind}` keeps its meaning: each watch the
reflector opens after its first counts as a restart. The first watch
is the streaming list.

`kubernetes/watch.go`, its two test files, and the loop's timing
constants are gone. The `kubernetes.Client` still sends every write,
and every read that a copy cannot answer.

### The machine operator's watches

Each watch is scoped to the objects this machine reads, so no other
machine's writes reach the pod:

| Kind | Selector | Wakes the loop |
|---|---|---|
| `Machine` | `metadata.name=<machine>` | on every change: the grant and the Lost verdict are status writes |
| `Node` | `metadata.name=<machine>` | on every change |
| `Pod` | `app=liken-machine-operator`, `spec.nodeName=<machine>`, in `liken-system` | on every change |
| `ResourceSlice` | `metadata.name=<machine>-liken.sh` | never: this operator is its only writer, and the ticker pass walks sysfs anyway |
| `Cluster` | `metadata.name=<cluster>` | on a spec edit only: the cluster operator writes its status after every change in the fleet |
| `Secret` | `metadata.name=registry-credentials`, in `liken-system` | on every change |

A machine with no cluster document opens neither of the last two.
Each watch also wakes the loop once when its first read is done. A pass
reads the newest copy of its `Machine` at its start, from the copy.

The pass reads the other objects through a `reader` (`watches.go`):
each read answers from the copy, or from the API server when the copy
cannot answer. The reads that stay direct, and why:

* The `Machine` read after a status write that lost with a conflict,
  in `publishOwnStatus`. The copy can still lag behind the write that
  won.
* The `HelmCharts` and the `LoadBalancer` `Services` of the retraction
  barrier. They are read only while a retraction waits, and a watch of
  every `Service` in the cluster would cost each machine far more.
* The pods on the node, which the drain and the imports trial list.
  Both run only during a drain or a proving boot.
* The seeding reads at start, and the DRA plugin's reads of a claim,
  which the kubelet names in its request.

The Node's `NodeHealthy` mirror, the node labels and taints, and a
person's cordon all react at once now, because the `Node` watch wakes
the loop. The ticker stays at ten seconds as the heartbeat's clock and
the backstop for the sysctls, `/etc/hosts`, the sysfs walk, and a
finished download, none of which send an event.

### The heartbeat renews without a read

`kubernetes.Heartbeat` holds the `Lease` as this process last wrote it,
and renews from that copy's `resourceVersion`: one `PUT` and no `GET`.
A pass between two ticker passes sends nothing, because the held copy
says the lease is fresh. A conflict or a 404 drops the copy, and the
same call reads the lease again and renews it. The first renewal of a
process reads, because it holds no copy yet. The heartbeat stays a
heartbeat: it is not an election, and nothing else writes a machine's
lease.

### The cluster operator's watches

| Kind | Scope | Wakes the loop |
|---|---|---|
| `Machine` | the whole fleet | on every change, as before |
| `Cluster` | the one `Cluster` | on a spec edit only: this program writes its status |
| `Lease` | `liken-system` | never: the ticker is the clock that ages a heartbeat |
| `DaemonSet` | `liken-system` | on a spec edit only: a leader's boot writes a new template |
| `Pod` | `app in (liken-machine-operator,machine-logs)`, in `liken-system`, indexed by `app` | never: the steward acts on a machine's version, which arrives as a `Machine` change |

The `DaemonSet` copy serves the rollout gate, the steward, and the
feature janitor, which read the same collection three times on each
sweep before. The `Clusters` copy also replaces the polling in
`awaitCluster`: the first `Cluster` wakes the loop, and a ten-second
retry reads the API server only while the copy cannot answer.

Two reads stay direct. The flux deploy key `Secret` is read only while
the flux feature is declared, and the permission to read it arrives
with the feature's own manifests, so a watch would be refused on every
fleet without GitOps. The `flux-system` `Namespace` is read only while
the feature is not declared, and this program may read that one
`Namespace` by name only. The engine probe keeps its one read a minute.

**The copies prove they are current.** Every watch of the process
shares one HTTP/2 connection. When the API server behind it loses
power, the stream goes quiet, and client-go closes it only about 45
seconds after the last frame. A sweep that judged heartbeats from the
frozen `Lease` copy would mark live machines Lost after 40 seconds,
through a write that reaches a live API server on another connection.
The leader election renews this program's own `Lease` every five
seconds, and the `Leases` copy receives each renewal. So the sweep
reads a copy only while the copy's view of that renewal is younger
than 15 seconds (`fleetReader.current`), and reads the API server
otherwise. client-go gives the election and the watches one shared
HTTP/2 connection, so a stalled connection stops the renewals too:
the write guard refuses every write after 10 seconds, and the process
exits when the election gives up, 15 seconds after the last renewal.
The next candidate takes over.

The ticker stays at ten seconds. It is a clock: a heartbeat ages into
a Lost verdict and a granted turn ages into a stalled rollout with no
event, and the channel poller and the engine probe keep their own
intervals.

### The reconcile loop

Both operators keep their pass model: one full pass for each wake, with
the wakes merged in a channel of one slot. A work queue for each object
(`util/workqueue`) would not make either loop simpler. The machine
operator judges one machine, and the cluster operator's verdicts are
about the whole fleet at once, such as a budget and a headcount, so
neither has a per-object unit of work. Neither pass has a retry of its
own: a failed pass waits for the next wake or tick, as before. The
cluster operator's pass is `operate`, apart from `main`, so a test runs
the loop.

### Leader election

`cluster-operator/leader.go` runs client-go's `leaderelection` with a
`resourcelock.LeaseLock` named `liken-cluster-operator` in
`liken-system` and `ReleaseOnCancel: true`. It copies
`media-operator`'s design and its release fix:

* **Timings.** `LeaseDuration` 30 s, `RenewDeadline` 10 s,
  `RetryPeriod` 5 s. After its last renewal at T, a leader gives up at
  T+15 s and exits by T+25 s, after client-go's release attempt. A
  waiting copy takes the `Lease` 30 s after it saw the last renewal, so
  after T+30 s. With client-go's 15-second duration the two times
  would meet.
* **The release fix.** A renewal that the cancel did not stop can land
  after client-go's release read the `Lease`, and the release then
  fails with a conflict. `end` reads the `Lease` again and clears it
  when it still names this process (`clearIfHeld`), so a waiting copy
  takes it on its next retry, not after 30 seconds.
* **Identity.** The pod's hostname, which is its name, and a random
  suffix, so a restarted container is a new candidate. No downward API
  variable is needed.
* **The order of a shutdown.** `SIGTERM` ends the loop after the sweep
  in flight. The sweep is the only part of the program that writes, so
  `stepDown` then releases the `Lease` at once. A loss exits the
  process with code 1, and the kubelet restarts it as a new candidate.
* **The write guard.** `kubernetes.Client.GuardWrites` makes every
  write the client sends ask `mayWrite` first. It allows a write only
  while this copy leads and its last accepted renewal is younger than
  the renewal deadline. A wrapper on the lock (`renewalClock`) records
  when this process sent each renewal that the API server accepted.
  The guard refuses writes from T+10 s, before the elector gives up.
* **A bound on each request.** The in-cluster client now has a total
  request timeout of 15 seconds, since no watch runs through it. A
  write the guard allowed before T+10 s is abandoned by the client
  before T+25 s, and a new leader starts after T+30 s.
* **The Deployment.** `RollingUpdate` with `maxSurge: 1` and
  `maxUnavailable: 0`, and `replicas: 1`. The manifest's comment says
  two replicas are safe once every node runs a release with the
  election. `terminationGracePeriodSeconds` is 60, so a shutdown can
  finish its sweep and its release. A waiting copy opens no watch and holds no
  copy of the fleet: the watches start when it takes the lead.
* **RBAC.** The `Role` in `liken-system` grants `create` on `leases`
  and `update` on the one name. RBAC checks a create before the object
  has a name, so create cannot be limited to it.
* **Acting without an election while the `Lease` is refused**
  (`unelected.go`). See the next section.

### A copy under another release's RBAC

The OS images carry the operator binaries, and a leader's boot applies
the manifests. The image tag `:installed` resolves on each node to the
build that node's OS carries. So this binary can run under the RBAC of
a release from before the election, which grants no write of the
`Lease`. A rollback from this release reaches that state on its normal
path: the spec's version is older than the applied template, so the
gate sends a leader first, its boot applies the old manifests, and the
`Deployment`'s new pod most likely lands on a node that still runs this
build. A copy that waited for the `Lease` there would grant nothing,
and the rest of the rollback needs its grants. The forward direction
reaches the same state in a rarer case: a worker upgrades first, which
the gate allows when no leader can take a turn, and the pod moves onto
it.

So a copy acts without an election while the API server refuses the
`Lease` itself: a 403 Forbidden on any request of the `Lease`, which
says the RBAC is missing, or a 404 NotFound on its create, which says
the `Lease` resource type is missing. Any other error (a timeout, a
5xx, a conflict, or a 404 on an update, which means the `Lease` was
deleted between the read and the write) is an ordinary failure of the
election, and the copy does not act. A wrapper on the lock classifies
each request of the elector, so the elector's own retry is the probe.

While a copy acts without an election:

* It logs one line when it enters the mode, and one when it leaves it.
* `liken_cluster_operator_unelected` is 1, and 0 otherwise.
* The write guard allows its writes.
* The copies never prove themselves current, because the copy cannot
  renew its own `Lease`, so every sweep reads the API server, as the
  releases before this one did.
* The elector tries the `Lease` again every retry period, 5 to 11
  seconds. When the `Lease` answers, the copy takes it and leads, or it
  finds a holder that renewed within the `Lease`'s duration, stops
  acting, and waits.
* A leader whose renewal gets one 403 enters the mode, and its next
  successful renewal ends it, so the write guard is off for no longer
  than one retry period.

The mode starts late on a rollback. The older RBAC grants `get` on
`leases`, so the new pod reads the `Lease`, which still names the
leader of the newer release with a fresh renewal. The pod waits the
`Lease`'s duration, and only its update then gets the 403 that starts
the mode: 30 to 41 seconds after the pod starts.

The trigger is a 403 on the `Lease`, and a 403 does not always mean
the RBAC is missing. An API server that just restarted may answer 403
until its RBAC cache has synced. A waiting copy that met such an
answer would act next to the leader for up to one retry period. This
is not confirmed for k3s, and the drill below checks it.

Two copies can act at once for up to one retry period in this mode: a
copy learns that another copy took the `Lease` only at its next try.
That equals the old behavior. Every release before the election ran
the cluster operator with no election at all, one replica, and the
`Recreate` strategy, and a second copy acted alongside the first
whenever one ran, for as long as it ran. The mode exists only under
that older release's RBAC, and it shortens the overlap to one retry
period instead of widening it.

The election links client-go's typed clientset, which costs about
13 MB of binary beyond the watches. The cluster operator's binary serves no other role: it
runs only as this `Deployment`, and no DaemonSet, per-workload pod, or
`init` runs it. The machine operator is a separate main package and does
not import `leaderelection`. `go list -deps` confirms that the typed
clientset reaches only `cluster-operator`.

The election is not fencing. A leader that pauses, for example on a
stalled node, can resume after its `Lease` expired and finish a
request it had already sent.

### What the election answers in the open problem

**Two instances.** Only the copy that holds the `Lease` watches and
writes. A waiting copy's client refuses every write, because `mayWrite`
refuses until the lead starts.

**Stop new work when leadership is lost.** The guard refuses every
write from T+10 s. The elector exits the process by T+25 s. The loop
does not start until the lead does.

**Bound requests.** The 15-second request timeout, with the guard,
abandons every allowed write before T+25 s.

**Stale writes.** Each write the sweep makes, when a paused former
leader sends it from an old view of the fleet:

| Write | Precondition | A stale write |
|---|---|---|
| A Lost verdict | the `Machine`'s `resourceVersion` | conflicts when the machine wrote since; otherwise the machine's next status write replaces it |
| The `Cluster`'s status | the `Cluster`'s `resourceVersion` | conflicts when the new leader wrote since; otherwise the next sweep replaces it |
| A grant taken back | the `Machine`'s `resourceVersion` | conflicts when the grant or the machine changed since; otherwise the decision still holds |
| A grant | the `Machine`'s `resourceVersion` | **can exceed the budget**: the precondition covers only that one `Machine`, and the new leader can grant another machine in the same window |
| An eviction by the steward | the pod's name | a pod the new template already replaced answers 404 |
| A delete by the feature janitor or the flux teardown | the object's name | can remove a feature's workload that a person enabled again within those seconds |

**Partial grant sequences.** `carryOutRollout` writes the grants of one
decision one `Machine` at a time. A crash between two writes leaves a
subset of a decision that was inside the budget, so the budget holds.
Each grant is a condition on its `Machine`, and the next leader counts
it as a slot in flight when it decides again.

**A paused former leader.** Everything above holds for a leader that
runs. A leader that pauses between the guard's check and the send, or
with a request in flight, can land a grant after a new leader decided
from a view without it. Per-object preconditions do not make the
budget a transaction. This is the concern that stays open.

## Measurements

**Requests for each settled pass**, counted by the tests' fake API
servers (`TestASettledPassOverTheCopiesSendsNoReads`,
`TestASettledSweepOverTheCopiesSendsOneRead`):

| | Before | After |
|---|---|---|
| Machine operator, a ticker pass | 8: six reads, the `Lease` read, and its renewal | 1: the `Lease` renewal |
| Machine operator, a pass between ticks | 7 | 0 |
| Cluster operator, a sweep (flux not declared) | 10 | 1: the `flux-system` `Namespace` |
| Cluster operator, a sweep (flux declared) | 10, and the engine probe once a minute | 1: the deploy key `Secret`, and the probe once a minute |

A machine sends about 360 requests an hour in place of about 2,900.
The tests also check that a pass over the copies publishes the same
status as a pass that reads the API server, and a separate test holds
the converter to `encoding/json`'s answer for every field of each
struct the passes read from a copy.

**Binaries.** Stripped with `-s -w`, built with Go 1.27.1:

| | Before | After |
|---|---|---|
| `liken-machine-operator` | 15,069,344 bytes, 358 packages | 19,792,032 bytes, 478 packages |
| `liken-cluster-operator` | 10,432,672 bytes, 274 packages | 28,172,448 bytes, 740 packages |

The machine operator pays for `tools/cache` and `dynamic` only. The
cluster operator pays for those and for the typed clientset that
`leaderelection` links.

**Idle RSS.** A harness binary for each operator linked every package
of the operator and ran only its watches, against a fake API server on
the same laptop: the old harness ran `WatchMachines`, and the new one
ran the operator's own watch setup, and the cluster operator's also
held the leader election. The fake held ten `Machines` and their
heartbeats, one `Cluster`, and the objects of one machine. `VmRSS` from
`/proc/<pid>/status` 45 seconds after the start, in two runs:

| | Before | After |
|---|---|---|
| Machine operator, six watches | 11.9 to 12.8 MB | 20.2 to 21.9 MB |
| Cluster operator, five watches and the election | 9.4 to 10.1 MB | 23.7 to 26.9 MB |

The machine operator runs on every machine, so each 1 GB machine gives
about 9 MB more to it.

## Tests

| What | Test |
|---|---|
| An object that does not convert is an error that names it | `TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt` |
| A copy that never started, or whose watch is refused, cannot answer | `TestACollectionThatNeverStartedCannotAnswer`, `TestACopyWhoseWatchIsRefusedCannotAnswer` |
| Through the reflector: the synced copy answers by name, by absence, as a list, and by label, with no `managedFields` | `TestTheSyncedCopyAnswersReads` |
| Through the reflector: the list and each watch carry the selectors | `TestEveryRequestCarriesTheSelectors` |
| Through the reflector: a watch opened again counts as a restart | `TestAWatchOpenedAgainCountsAsARestart` |
| Through the reflector: the first read wakes the loop once | `TestSyncedRunsOnceAfterTheFirstRead` |
| Through the reflector: each wake rule, for a status write, a spec edit, a deletion mark, a new object, and a removed object | `TestEachHandlerWakesTheLoopForItsChanges` |
| An unchanged update, a removal with no copy, and an object that does not convert | `TestARereadWithNoChangeDoesNotWake`, `TestARemovalWithNoCopyWakes`, `TestAnObjectThatDoesNotConvertDoesNotWake` |
| The copies decode like `encoding/json` | `TestTheCopiesDecodeLikeADirectRead` |
| A copy does not answer until it holds this process's write, answers at once when it already does, and settles a write with an unknown outcome | `TestACopyDoesNotAnswerUntilItHoldsThisProcessesWrite`, `TestAWriteTheCopyAlreadyHoldsKeepsItAnswering`, `TestAWriteWithAnUnknownOutcomeSettles` |
| A status write answers the written version | `TestPublishStatusAnswersTheWrittenVersion` |
| A sweep right after its own grant reads the fleet from the API server and grants no second turn | `TestASweepRightAfterItsOwnGrantReadsTheFleetDirectly` |
| A later version settles a write record | `TestALaterVersionSettlesAWrite`, `TestAWriteOvertakenByALaterChangeKeepsTheCopyAnswering` |
| The copies answer only while the leader `Lease` in them is fresh | `TestTheCopiesAnswerOnlyWhileTheLeaderLeaseInThemIsFresh` |
| Requests of a settled pass and a settled sweep | `TestASettledPassOverTheCopiesSendsNoReads`, `TestASettledSweepOverTheCopiesSendsOneRead` |
| The heartbeat renews with no read, and reads again after a conflict or a 404 | `TestAHeldLeaseRenewsWithNoRead`, `TestAHeldLeaseThatChangedIsReadAgain` |
| A guarded client refuses writes and not reads | `TestAGuardRefusesWritesAndNotReads` |
| The write guard allows only a fresh leader | `TestTheWriteGuardAllowsOnlyAFreshLeader` |
| A step down releases the `Lease`, also after a late renewal, and leaves it to expire when the release is refused | `TestAStepDownReleasesTheLease`, `TestAStepDownReleasesTheLeaseAfterALateRenewal`, `TestAStepDownWhoseReleaseIsRefusedLeavesTheLeaseToExpire` |
| A leader that cannot renew, or whose `Lease` another process took, exits | `TestALeaderThatCannotRenewExits`, `TestALeaderWhoseLeaseAnotherProcessTookExits` |
| A waiting copy takes a released `Lease` at once | `TestAWaitingCopyTakesAReleasedLeaseAtOnce` |
| The loop finishes the sweep in flight at a shutdown | `TestOperateFinishesTheSweepInFlightAtAShutdown` |
| A 403 on the `Lease`, or a 404 on its create, makes a copy act without an election and sets the gauge; a 500 does not | `TestOnlyARefusedLeaseMakesACopyActWithoutAnElection` |
| A `Lease` that answers again is taken, and the gauge clears | `TestALeaseThatAnswersAgainIsTaken` |
| A copy without an election stops acting when another copy leads | `TestACopyWithoutAnElectionStopsWhenAnotherCopyLeads` |
| A leader's refused renewal ends the mode at its next successful renewal | `TestARenewalThatAnswersAgainEndsTheMode` |
| A write the guard refused, a 404, and a conflict leave the copy answering | `TestAWriteThatWroteNothingLeavesTheCopyAnswering` |

## Considered and set aside

* **A work queue for each object.** Neither pass has a per-object unit
  of work (see "The reconcile loop").
* **Watching the retraction barrier's `HelmCharts` and `Services`.** A
  watch of every `Service` in the cluster on every machine, for a read
  that happens only while a retraction waits.
* **Watching the flux deploy key `Secret`.** Its permission comes and
  goes with the feature, so the watch would be refused on most fleets.
* **Warm copies in a waiting cluster operator.** A waiting copy that
  watched the fleet would take over a few hundred milliseconds sooner,
  and would double the watches and the memory of every standby.

## What is still open

**A fence for the grants.** The open problem stays open for one
concern: a paused former leader can write a grant after a new leader
decided without it, and the two grants together can exceed the budget.
The options:

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

The recommendation is option 1: it is a true compare-and-swap on one
object, and the machine operator does not change.

**The first rollout.** The release that carries this change rolls the
`Deployment` with the new strategy, and the tag resolves on each node,
so a pod on a node that still runs the previous release acts without
an election. Until every node runs this release, a pod with the old
build and a pod with the new one can both act.

## The drill still owed

On liken-1, with both operators on a build of this change:

1. Count the API requests of a settled machine and of the sweep, from
   the API server's audit or its metrics, and compare them with the
   tables above.
2. Read each operator container's working set, and compare it with the
   build before.
3. Scale the cluster operator to two replicas, delete the leader's pod,
   and time the takeover. Roll the `Deployment` and check that the new
   pod takes the `Lease` within about eleven seconds.
4. Pause the leader's process beyond the `Lease`'s duration, let the
   other copy take over, resume the old one, and check that it exits
   and that its writes are refused.
5. With two replicas, restart the API server the waiting copy uses,
   and check whether the waiting copy ever logs that it acts without
   an election.
6. Roll back from this release and time how long the new pod takes to
   act without an election, and check `liken_cluster_operator_unelected`.
