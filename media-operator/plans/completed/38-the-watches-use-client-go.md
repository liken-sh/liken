# 38, The watches use client-go

Built on 2026-09-27. Every Kubernetes watch in the operator and in the
api role runs on client-go's reflector, and the hand-written loop in
`watchloop.go` is gone. The operator's pass reads every collection it
needs from the watches' memory, so a pass on a settled cluster sends
the API server no request. On the test fixture below, a settled pass
sent 20 reads before this plan and sends none now. Against a k3s API
server holding 250 `Player`s, the operator sent 1,536 requests a
minute at rest before this plan and none after it. The numbers come
from the test's fake API server and from a k3s API server in Docker,
not from `liken-1`. The drill that is still owed is at the end of this
plan.

## The problem

The operator watched seven collections and the api role watched five
named objects through one loop written by hand in `watchloop.go`, with
335 lines of tests for the loop alone and 917 more for the watches that
ran on it.
Seven other repositories in the organization each had a loop of the
same kind. Reviews in 2026-09 found the same faults in several of them,
and each fix landed in one repository at a time. On 2026-09-27 the
organization chose client-go's reflector for every operator, in the
lean form that the `operators` skill in `.agents` records.
`bluetooth-operator` plan 09 is the reference port.

The same day's API audit ranked the operator's 10-second pass third in
the organization, at about 9,000 requests an hour on a home cluster.
Each pass listed the Plays, the Players, the Remotes, the Keymaps, the
Peripherals, every `ResourceSlice` in the cluster, and the Receivers,
and sent a GET for each unit's claims, its pods, and its `Display`. The
watches already held most of that state, and the pass read it again
from the API server.

## The watches

A search for every watch in the repository found twelve. Each one
moves.

| Process | Collection | Scope | What a change does |
|---|---|---|---|
| operator | `Play` | every namespace | wakes the pass |
| operator | `Player` | every namespace | wakes the pass |
| operator | `Remote` | every namespace | wakes the pass |
| operator | `Keymap` | the cluster | wakes the pass |
| operator | `MediaPreferences` | the cluster | wakes the pass |
| operator | `Peripheral` | the cluster | wakes the pass |
| operator | `Pod` | every namespace, `media.liken.sh/component in (playback,idle,remote)` | wakes the pass when a playback pod goes away or turns `Failed` |
| api | `ConfigMap` `media-api-ca`, `display-api-ca`, `audio-api-ca` | `fieldSelector=metadata.name=`, one watch each | rebuilds the sibling trust pool |
| api | `ConfigMap` `kube-system/extension-apiserver-authentication` | `fieldSelector=metadata.name=` | takes up a rotated client authority |
| api | `Secret` `media-api-tls` | `fieldSelector=metadata.name=` | serves a pair another writer put there, or mints one again when it is gone |

The operator adds four watches that wake nothing and only keep the
pass's reads in memory: `ResourceClaim` and `ResourceSlice` in the
whole cluster, and `Display` and `Receiver` in the whole cluster. The
pods watch covered only the playback pods before, and it now selects
the idle pods and the Remotes' reader pods too.

Several sources in the repository are not Kubernetes watches, and they
do not move: the bus subscriptions in `bus.go`, the controllers' input
nodes in `remote.go`, and mpv's IPC socket in `mpvipc.go`.

## The design

### What moved

`watch.go` holds `watchCollection`, which runs one watch on
`cache.NewInformerWithOptions` with the dynamic client, in the shape of
`display-operator`'s port. The operator and the api role import only
`k8s.io/client-go/tools/cache`, `k8s.io/client-go/dynamic`, and
`k8s.io/client-go/rest` for the watches. A transform removes
`metadata.managedFields` before the informer stores an object.
`convert` decodes an object into the operator's own struct with
`runtime.DefaultUnstructuredConverter`, unwraps a tombstone, and answers
an error that names an object that does not convert.

`clusterwatch.go` starts the operator's eleven informers after the
operator takes the `Lease`, and `watchCluster` waits until every one
has read its collection before the first pass. A pass that read an
empty collection would release every run and every standing pod it did
not read. The wait replaces the seven lists the operator sent at start.
A collection that is not read within a minute ends the process, the
way a list that failed at start did, and a signal during the wait ends
it too. Either way the operator releases the `Lease` before it exits.

`namedwatch.go` holds `watchNamed`, which follows one object under a
field selector and hands each version to its owner, and `nil` for an
absent object. The api role runs it for the three sibling CA
`ConfigMap`s, the client authority, and the serving `Secret`, and
waits up to 10 seconds for the first read of the four `ConfigMap`s
before it listens.

These parts are gone: `watchloop.go` and its tests,
`watchcollections_test.go`, the seven `watch*` functions,
`openWatch`, `Client.Do`, the list functions and list types the pass
no longer calls, and `ListMeta`. In all, the port removed 1,194 lines
of code and 1,534 lines of tests, and added 1,184 lines of code and
1,428 lines of tests. The counts include comments.

### The pass reads the view

`clusterview.go` holds the view: one informer store for each
collection, and accessors that answer the operator's own structs in
the order the API server lists them. `ListPlays(client)` became
`o.view.Plays()`, `GetPlayer(client, ...)` became `o.view.Player(...)`,
and so on for every read the pass makes. An absent object answers
`ErrNotFound`, the answer the client gives, so each caller keeps its
branches. An object that does not convert fails the read of its
collection with an error that names it, the way a list the API server
answered with a bad object failed before.

The view can be a moment behind the API server. A pass that only
compares reads it and acts on nothing, and the change it has not seen
yet reaches the next pass. A pass that is about to create, delete, or
recreate must read the object again, because the create or the delete
this operator sent on the last pass can still be on its way to the
store. So:

* A run reconciles from the view first. It is settled when its pod
  stands, is not `Failed`, and holds the claim and the Remotes the
  current Player produces, or when the pod is on its way out, and a
  settled run costs no request. Every other run reconciles again from
  the API server: it reads its `Player` and its `Remote`s with GETs,
  and `ensurePlayback` reads the pod and the claim and runs the same
  branches as before.
* `reconcileStanding` decides from the view first. A pair the view
  shows as the pass would build it costs no request. A pair that needs
  a create or a delete is read again from the API server, and the rule
  acts on that read.
* The second read covers the `Player` and the `Remote`s because each
  collection has a watch of its own, and nothing orders one watch's
  events against another's. A `Play` applied in one file with its
  `Player` and its `Remote` can reach the view before them, and a
  `Player` edit can reach it after the pod the edit reshaped. From the
  API server's answer the pass never fails a `Play` for an object that
  exists, and never builds or compares a pod against a `Player` spec
  that an edit already replaced.
* So a settled pass is free only while every run is settled. A run
  that is not costs a GET of its `Player`, one GET for each `Remote`,
  and the reads `ensurePlayback` makes, on each pass: a `Play` whose
  `Player` or `Remote` does not exist, a run whose pod waits out the
  recreate backoff, and a run whose pod is being replaced. Each state
  lasts until a person fixes the `Play`, or until the backoff or the
  replacement ends.
* A status write names the `resourceVersion` the pass read, so a pass
  that reads a `Play` from before its own last write meets a conflict,
  reads the `Play` again, and finds the status already written.
  `writePlayStatusFrom` answers the phase that second read found, and
  `writePlay` logs a phase change and counts it on
  `media_playback_starts_total` or `media_playback_failures_total` only
  for a write that happened, from the phase the API server held. The
  pass counted the phase itself before, from the `Play` it read.
* The pass keeps its direct reads where it acts on its own last write:
  the pod read after a delete in `replace` and `releasePlay`, the claim
  read in `finishReplacement`, the pod read after a 409, and the
  object read after a conflicting status write.

Older releases created pods that carry no label the pods watch
selects. A Remote's reader pod now carries
`media.liken.sh/component: remote`, and a reader pod an older release
created without it is labeled in place by the first live read, so the
release rolls no reader pod for the label. A pod the pass wants gone,
such as the idle command pod that releases before 2026.09.02-002
created, is read from the API server even when the view does not hold
it, until one read finds it absent. `absenceChecked` records that read,
and the pass trusts the view for that pod from then on in the process.
Nothing in this release creates a pod without the label, so one absent
read is enough.

### What wakes the pass

The rule for each watch is the one the hand-written loop had. A change
to a `Play`, a `Player`, a `Remote`, a `Keymap`, a `MediaPreferences`,
or a `Peripheral` wakes the pass, and that includes this operator's own
status writes: the pass after a status write retires a Finished Play at
once and publishes a new phase to the `Player`. A playback pod wakes
the pass when it goes away or turns `Failed`. After a gap in the watch,
the informer reports each difference from what it held, so a playback
pod that failed during the gap arrives as an addition, and the handler
wakes the pass for an added pod that is already `Failed`. The four
watches that only fill the view, and the idle and reader pods, wake
nothing. Their changes reached the pass on the backstop tick before,
and they still do.

### The reconcile loop and the tick

The loop keeps its model: one full pass for each wake, wakes merged in
a channel with one slot, and the 10-second tick. A work queue for each
object would rewrite a working pass, and a pass that reads memory costs
nothing to run whole.

The tick stays at 10 seconds, and its comment names what it is. As a
clock it ends three waits that no event ends: a position that only
advanced reaches the `Play`'s status (`positionWriteInterval`), a
Finished Play goes when its window passes, and the first level or focus
mark of a new broker session goes out when `catchUpGrace` ends. As a
backstop it is when a pass reads the claims, the Displays, the
ResourceSlices, the Receivers, and the standing pods, whose changes
wake nothing. The watches keep those in memory, so a tick on a settled
cluster costs no request. The recreate backoff still schedules its own
wake with `requeueAfter`, and its bound is unchanged.

### An optional resource

`display-operator` defines `Display` and `equipment-operator` defines
`Receiver`, and a cluster can run this operator without either. The
pass read an absent resource as one with no objects, from the API
server's 404. A watch of an absent resource marks it `optional`: its
list answers an empty collection on a 404, so the informer reads it at
once, and its watch answers a quiet stream that ends after five
minutes with a `410 Gone`, so the reflector lists again and watches
from the version that list returns. Nothing the operator watches
reports that a resource was installed, so the five minutes is the one
clock in the watch code, and its comment says so. Without it the
reflector would back off and log a failure every 30 seconds for as long
as the resource is absent. A `Peripheral` is not optional, because the
operator refused to start without it before.

### The pod build

The api role runs from the pod build, the build every playback pod's
command sidecar and every Remote's reader runs. Its watches link
client-go's reflector, dynamic client, and rest into that build. The
Makefile check allowed no client-go package there. It now fails when
the pod build links `k8s.io/client-go/kubernetes`, `informers`,
`dynamic/dynamicinformer`, or `tools/leaderelection`. The cost is in
the measurements below.

### The shutdown grace

The `Deployment` sets `terminationGracePeriodSeconds: 60`. A shutdown
finishes the pass in flight, waits up to 5 seconds for the bus session
to end, and then releases the `Lease`: up to 11 seconds for client-go's
election to end, and up to 5 seconds more for the release that follows
a late renewal (`leader.go`). That is up to 21 seconds after the pass,
and the default 30-second grace would leave the pass 9 seconds. A pass
that writes waits on the API server for each write, each bounded by the
client's 10-second header timeout. The manifest's comment holds the
same arithmetic.

### RBAC

The `ClusterRole` gains `list` and `watch` on `resourceclaims`,
`watch` on `resourceslices`, and `list` and `watch` on `displays`. The
pods rule already granted `patch`, which labels an older reader pod.
The api role's `Role` already granted `list` and `watch` on its
`ConfigMap`s and its `Secret` by name.

### The reflector and the organization's guards

The reflector does not meet the three guards in the `operators` skill
exactly, in the five ways that bluetooth-operator plan 09 lists. None
of them loses an event, and the organization accepts them.

## Measurements

### Requests per pass

`TestASettledPassSendsTheAPIServerNothing` settles a Player whose
screen is wired through a Receiver, a bonded Remote, and a running
Play, then runs one pass with the view that `watchCluster` filled from
a scripted API server, and counts the requests the pass sent.

| | Before | After |
|---|---|---|
| Lists | 7: Plays, Players, Remotes, Keymaps, Peripherals, ResourceSlices, Receivers | 0 |
| GETs | 13: the default `MediaPreferences`, the `Player`, the `Remote`, the `Display`, five claim reads of three claims, four pods | 0 |
| Writes | 0 | 0 |
| All requests | 20 | 0 |

At one tick every 10 seconds, the 20 reads were 7,200 requests an hour
for that one unit, before any wake.

The operator ran against a k3s v1.36.3 API server in Docker, with the
media, bluetooth, and equipment CRDs, no `Display` resource, no idle
display class, and 50 or 250 `Player`s with a sink each. It held the
`Lease` and reached no broker. The count is every request other than a
watch on the `media.liken.sh`, `bluetooth.liken.sh`,
`equipment.liken.sh`, and `resource.k8s.io` groups and on pods, from
the API server's `apiserver_request_total`, over 60 seconds starting
45 seconds after the operator started, in two runs of each build.

| | Before | After |
|---|---|---|
| 50 `Player`s, requests a minute | 336, 336 | 1, 0 |
| 250 `Player`s, requests a minute | 1,536, 1,536 | 0, 0 |

Before this plan each pass read every collection and each `Player`'s
idle claim, six passes a minute. The same runs show the optional
resource on a real API server: the operator started and passed with no
`Display` resource installed, and logged no failure.

### Binaries and memory

Built with `CGO_ENABLED=0 go build -trimpath`, the way the `Dockerfile`
builds them, and stripped with `-ldflags="-s -w"` for the second
figure. The memory of the pod build is the command role 5 seconds
after it starts with no broker to reach, in three runs of each build on
the laptop. The operator's memory is its process 105 seconds after it
starts against the k3s API server above, in two runs of each build and
a third run of the final build at 250 `Player`s.

| | Before | After |
|---|---|---|
| Operator build, as the image holds it | 40,120,826 bytes | 41,548,358 bytes |
| Operator build, stripped | 28,262,560 bytes | 29,118,624 bytes |
| Operator build, linked Go packages | 726 | 727 |
| Pod build, as the image holds it | 18,102,920 bytes | 22,704,326 bytes |
| Pod build, stripped | 12,390,560 bytes | 15,593,632 bytes |
| Pod build, linked Go packages | 271 | 393 |
| Pod build, command role, `VmRSS` at start | 10.3 to 10.6 MB | 14.6 to 14.9 MB |
| Pod build, command role, `RssAnon` at start | 2.1 to 2.3 MB | 2.5 to 2.8 MB |
| Operator, idle, 50 `Player`s, `VmRSS` | 29.6 to 30.2 MB | 31.3 MB |
| Operator, idle, 50 `Player`s, `RssAnon` | 10.4 to 11.2 MB | 10.5 MB |
| Operator, idle, 250 `Player`s, `VmRSS` | 31.4 to 31.5 MB | 34.3 to 34.8 MB |
| Operator, idle, 250 `Player`s, `RssAnon` | 12.3 MB | 13.4 to 14.0 MB |

Most of the pod build's growth is file-backed: the pages of the larger
binary that start-up reads. The anonymous memory, the heap and the
stacks, grows by about 0.5 MB. A kubelet's working set counts little
of a binary's file pages, which is why the reader measured 3.4Mi of
working set on a home cluster while its `VmRSS` on the laptop is near
10 MB. The command sidecar's request is 12Mi against a measured 11Mi at
the 90th percentile, so the drill below reads its working set again.

The operator's stores hold each object it watches, so its memory grows
with the cluster: at 250 `Player`s, 1.1 to 1.7 MB more anonymous
memory than the build before it. The `Deployment`'s 64Mi limit was set
before the stores, from an estimate near 30Mi, and the drill below
reads the working set on a cluster with its real claims and pods.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The pass tests read a view that the fake cluster
serves from its maps, so a test that changes the cluster between two
passes changes what the next pass reads.

| What | Test |
|---|---|
| A settled pass, reading the view `watchCluster` filled, sends no request | `TestASettledPassSendsTheAPIServerNothing` |
| Through the reflector: a change to a `Play`, a status write included, wakes the pass | `TestAChangeToAPlayWakesThePass` |
| Only a playback pod that goes away or is `Failed` wakes the pass; idle and reader pods do not | `TestAPodWakesThePassOnlyWhenAPlaybackPodEnds`, `TestAPodRemovedWithNoCopyWakesThePass` |
| Through the reflector: the pods watch selects by the component label | `TestThePodsWatchSelectsTheOperatorsOwnPods` |
| Each watch the API server accepts after the first counts as a restart | `TestAWatchCountsEachReopen` |
| An absent optional resource reads as empty, and the view takes it up when it arrives | `TestAnAbsentOptionalCollectionReadsAsEmptyUntilItArrives` |
| A collection the operator cannot read ends the first read with its name | `TestTheFirstReadNamesACollectionItCannotRead` |
| The view reads an informer's store, in the API server's order, and converts whole | `clusterview_test.go` |
| A pod the view has not seen yet is read from the API server and not created twice | `TestAPlaybackPodTheViewHasNotSeenIsNotCreatedAgain`, `TestAStandingPodTheViewHasNotSeenIsNotCreatedAgain` |
| A `Failed` pod the API server already holds on its way out is not replaced twice | `TestAFailedPodTheAPIServerAlreadyReleasedIsNotReplacedAgain` |
| An older reader pod is labeled in place, and the next pass reads it from the view | `TestAReaderPodWithoutTheComponentLabelIsLabeledInPlace` |
| An older unlabeled pod the pass wants gone is deleted, and read once | `TestAnUnlabeledPodThePassWantsGoneIsDeleted`, `TestReconcileIdleReadsTheRetiredPodOnceWhenNoneStands` |
| A `Play` whose `Player` or `Remote` the view has not seen yet starts | `TestAPlayWhosePlayerTheViewHasNotSeenStarts`, `TestAPlayWhoseRemoteTheViewHasNotSeenStarts` |
| A pass one write behind logs and counts no phase change | `TestAPassOneWriteBehindLogsAndCountsNoPhaseChange` |
| A pod built from a `Player` edit the view has not seen is kept | `TestAPodBuiltFromAPlayerEditTheViewHasNotSeenIsKept` |
| Through the reflector: a named object reaches its owner, absent, changed, and deleted, and a bad version is skipped | `namedwatch_test.go` |
| The trust store, the client authority, and the serving pair follow their objects through the reflector | `apitrust_test.go`, `apiclientca_test.go`, `TestTheServingPairFollowsTheSecret` |

No test drives `operate()` itself, so the start-up paths it adds are
proved only by reading: a signal during the first read steps down with
no pass, and a first read that fails releases the `Lease` and exits.

## Considered and set aside

* **Only the seven watched kinds in the view.** The cache rule reads
  from an informer that already holds an object. The pass's reads of
  claims, ResourceSlices, Displays, Receivers, and standing pods were
  most of its requests, and the pass reads each of them on every tick,
  so a watch pays for itself.
* **Waking the pass on the claims, the Displays, the Receivers, and the
  ResourceSlices.** A claim that the scheduler allocates, or a monitor
  that goes dark, would reach the status at once instead of on the next
  tick. Each of those watches receives changes this operator does not
  act on, and the tick already covers them, so this plan keeps the
  behavior it had.
* **A per-object work queue** (`util/workqueue`). The pass reconciles
  the whole cluster for each wake, and the wake channel already merges
  a burst.
* **A third build for the api role.** The command sidecar and the
  reader would link no client-go at all. The api image would hold its
  own build, and the `Dockerfile`s and the release would change. The
  pod build grows by about 0.5 MB of anonymous memory, so this plan
  keeps one pod build.
* **The claims watched by a label.** A claim this operator creates
  carries no label, and a label added now would reach only the claims
  created after it. An idle or a Remote's claim does not roll with a
  release, so the claims are watched in the whole cluster.

## The drill still owed

On `liken-1`, with the operator and the api on a build of this change:

1. Read the operator container's working set, and the command
   sidecar's and the reader's, and compare them with the build before
   this change and with their requests.
2. Read the API server's request rate from the operator's
   `ServiceAccount` at rest, and compare it with the audit's figure.
3. Start a `Play` and time the pod's creation, then delete the pod and
   check that the pass recreates it once, with one line.
4. Rotate a sibling's CA `ConfigMap` and check that media-api composes
   through the new CA with no restart.
5. Roll the `Deployment` and check that the new pod takes the `Lease`
   within about 11 seconds of the old pod's `SIGTERM`, and that the old
   pod logs the release before it exits.
