# The watches use client-go

Plan 10. Built on 2026-09-27. The watches moved to client-go's
reflector, a pass reads the kinds it does not write from the
informers' stores, and the tests run on the laptop. No drill has run
on a cluster yet, and the drill that is owed is at the end of this
plan.
The leader election that the organization asked for with this port
is not built. The section "One operator holds the `Lease`" states why
and gives the options.

## The problem

The operator watched the API server through one watch loop written by
hand in `watch.go`: `watchCollection`, `watchCollectionBy`, a backoff,
and a memory of each object's key, with 297 lines of tests for the
loop's guards alone in `watch_guards_test.go`. Seven other repositories
in the organization each had a loop of the same kind. Reviews in
2026-09 found the same faults in several of them, and each fix landed
in one repository at a time.

On 2026-09-27 the organization chose client-go's reflector for every
operator, in the lean form that the `operators` skill in `.agents`
records. The reason is maintenance: upstream maintains and tests the
reflector, so the project stops owning eight loops.
`bluetooth-operator` plan 09 is the reference port, and this port
copies its shape.

## The watches

A search of the source for `watch=true`, `watchCollection`, and every
caller of the watch functions finds nine watches: six in the
`Deployment` and three in the node workload.

| Workload | Collection | Started in | Wakes the loop on |
|---|---|---|---|
| `Deployment` | `Receiver` | `serve` in `reconcile.go` | every change, the status included |
| `Deployment` | `CECBus` | `cecBusController.run` | every change, the status included |
| `Deployment` | `Television` | `cecBusController.run`, once the kind lists | every change, the status included |
| `Deployment` | `Display` | `cecBusController.run`, once the kind lists | a new or removed object, or a move of `status.node` or `status.physicalAddress` |
| `Deployment` | `Receiver` | `cecBusController.run`, once the kind lists | a new or removed object, or a new `metadata.generation` |
| `Deployment` | `Television` | `awaitPowerRead` in `television_session.go`, for one power press | every change, until the answer arrives or `cecPowerReadWait` ends |
| node workload (`cec`) | `CECBus` | `cecNode.loop` | every change, the status included |
| node workload (`cec`) | `Television` | `cecNode.watchLater`, once the kind lists | every change, the status included |
| node workload (`cec`) | `Display` | `cecNode.watchLater`, once the kind lists | every change |

Every watch covers the whole cluster, because every kind here is
cluster-scoped, and no watch has a label or field selector.

These sources are not Kubernetes watches, and they did not move: the
receiver sessions (the Denon's TCP stream and the WiiM's UPnP events),
SSDP and mDNS discovery, the media bus over MQTT, and the CEC adapter
through the kernel's CEC API.

## The design

### What moved

`watch.go` now holds `watchCollection`, which runs one watch on
client-go's informer (`cache.NewInformerWithOptions`) with the dynamic
client. The operator imports only these parts of client-go:

* `k8s.io/client-go/tools/cache`, for the reflector and the informer.
* `k8s.io/client-go/dynamic`, which lists and watches a custom
  resource with no generated code.
* `k8s.io/client-go/rest`, for the connection's configuration.

It does not import `dynamicinformer`, `informers`, or `kubernetes`
(the typed clientset).

`Client.watcher` builds the dynamic client once, from the same address
and credentials as the operator's own `Client`: the ServiceAccount's
CA file and token file in a pod, and nothing in a test. client-go
reads the token file again as the kubelet renews it.

The informer hands each handler an `*unstructured.Unstructured`.
`convert` decodes it into the operator's own struct with
`runtime.DefaultUnstructuredConverter`, and unwraps the tombstone
(`cache.DeletedFinalStateUnknown`) that the informer sends for an
object deleted while the watch was down. An object that does not
convert is an error that names the object. The handler logs it and
wakes the loop, because it cannot tell what changed.

A transform removes `metadata.managedFields` from each object before
the informer stores it.

These parts are gone: `watchCollectionBy`, `watchMemory`,
`watchBackoff`, the watch clocks, `WatchReceivers`, `WatchCollection`,
`Client.Do`, `displayKey`, `generationKey`, and
`watch_guards_test.go`. `watch_test.go` no longer tests a loop.

### What stayed

The operator's own `Client` in `apiclient.go` sends every write, and
every read of a kind that the reader also writes. A handler still only
wakes the loop. The next section states which reads moved to the
informers' stores.

Each watch keeps the rule for when it wakes the loop, from the table
above:

* **Every change.** `wakeOnEvery` wakes the loop on each addition,
  update, and deletion. The `Receiver` loop reads `status.session`,
  which media-operator writes, so it wakes on status writes. The
  `CECBus` and `Television` loops of both workloads act on status that
  the other workload writes.
* **A mark.** `markHandler` wakes the loop for a new object and a
  removed object, and for an update only when the mark moved.
  `displayMark` is the `Display`'s node and physical address, and
  `receiverSpecMark` is the `Receiver`'s generation. Both marks also
  hold the UID. After a gap in the watch, an object that somebody
  deleted and created again with the same name reaches the handler as
  an update, and the new object can have the same generation or the
  same address as the old one.

Two parts changed shape:

* The keyed watches kept their own copy of each object's key, to tell
  a status write from an edit. The informer hands an update both the
  copy it held and the new copy, so the handler compares their marks
  and keeps no copy.
* The loop woke the pass after every list it made after a gap. After a
  gap, the informer reads the collection again and reports each
  difference from what it held as an addition, an update, or a
  deletion. So a watch that wakes on every change still wakes after a
  gap, and a watch with a mark wakes after a gap only when a mark
  moved or an object came or went. The rule is narrower, and it loses
  no change that the pass reads. The watch still wakes the loop once
  when an informer's first read is done, through the informer's
  `HasSyncedChecker`. The pass can read a collection before the watch
  does, and an object removed between the two reads is in neither the
  watch's read nor any event.

A watch of a kind whose definition can be missing still waits for the
kind to list: `startFollows` and `watchLater` start it at the first
pass whose list answers a version. A reflector on a kind with no
definition retries its own list and logs each failure, for as long as
the definition is missing.

`awaitPowerRead` no longer passes a version to its watch. It reads the
`Television` objects once, and again on each wake of its watch, and
the watch's first read wakes it too. It reads them from its watch's
store once the store has read, and from the API server before that.

### The pass reads the stores of the kinds it does not write

Each workload holds the store of an informer in a `watchStore`
(`watchcache.go`), and a pass reads a kind from the store instead of
listing it from the API server, when the pass does not write that kind.
The informer updates its store before it calls a handler, so the pass
that an event wakes reads that event's change.

A pass lists a kind that it writes from the API server. The event of
the pass's own write can arrive after a later wake, so a store can hold
the copy from before that write. A pass that compared its write with
that copy would write again: a status with a new `lastTransitionTime`,
a discovered `Television` or `CECBus` created a second time with a
second log line, or a setting sent to a receiver again by a new
`Receiver` unit. A comment at each such list says why it stays.

A pass reads the API server for a kind it does not write while the
store has nothing to give it: before the informer's first read is
done, after the watch stopped, for a kind whose definition is missing
and so has no watch yet, and when an object in the store does not
convert. A pass never acts on an empty store as if the collection were
empty.

| Pass | Reads from the API server | Reads from a store |
|---|---|---|
| The `Receiver` pass | `Receiver` | none |
| The `Deployment`'s `CECBus` and `Television` pass | `CECBus`, `Television` | `Display`, `Receiver` |
| The node workload's pass | `CECBus`, `Television` | `Display` |
| `awaitPowerRead`, on each wake | none, after its watch's first read | `Television`: it waits for the node workload's write |

`discovery.reconcile` and `televisionSessions.list` also keep their
API reads: each reads a kind it writes.

The loop keeps its pass model: one full pass for each wake, with
wakes merged in a channel of one slot. A work queue for each object
(`util/workqueue`) would split a pass that derives every `Television`
from every bus, and it would not make the code simpler.

The requests a pass sends to read, counted in the tests' fake API
servers:

| Pass | Before | After, once the watches have read |
|---|---|---|
| The `Receiver` pass | 1 list | 1 list |
| The `Deployment`'s `CECBus` pass | 4 lists | 2 lists |
| The node workload's pass, in `Control` with a display | 2 lists and 1 read of the `Display` | 2 lists |
| `awaitPowerRead` | 1 list on each wake | a list only before its watch's first read is done |

`TestTheCECBusPassReadsTheStores` counts the `Deployment`'s reads, and
`TestTheNodePassReadsFromTheStores` the node workload's. The section
"Every kind from the stores, with the memo" below takes the lists in
the table's right column away.

Each informer's store holds the whole collection, so the stores cost
memory in proportion to the objects. The `Deployment`'s two loops
share one informer on the `Receiver` objects and one on the
`Television` objects, so the process holds each object once.

### Every kind from the stores, with the memo

The organization chose one way to read from a store for every
operator, and this operator now follows it, with the same types and
functions as `audio-operator`, `bluetooth-operator`, and
`display-operator` in `objectcache.go`. A pass reads every kind from
the stores, the kinds it writes included, and lists from the API
server only while a store has nothing to give. The section above
records why the kinds this operator writes were listed. The memo
answers that reason instead:

* `versionMemo` records the `resourceVersion` of each object's newest
  copy that this process wrote or read. Every write goes through the
  `Client`, so the memos are the `Client`'s (`objectVersions`), and
  each apply, create, and delete notes its answer. A store's copy at
  another version is read from the API server once.
* Each store holds the whole collection, so a list also reads each
  object the process created that the store does not hold yet, and
  leaves out each object it deleted that the store still holds. A
  pass does not create a `Television` or a `CECBus` a second time.
* A copy in a store that does not convert is read from the API server,
  so a list leaves out no object.
* Every write is a server-side apply, a create, or a delete. An apply
  states no `resourceVersion`, so a stale copy never makes the API
  server refuse it, and the operator needs no retry from a fresh copy.

`TestAPassDoesNotActOnACopyOlderThanItsOwnWrite` holds every store at
a snapshot from before the pass's writes, and fails without the memo:
the `CECBus` status is written again, and the discovered `Television`
is created three times with three log lines.
`TestAReceiverReadAfterItsOwnStatusWriteAnswersTheWrite` covers the
`Receiver` a new unit reads its settled state from.

Requests to the API server for one settled pass, one that runs after
the watches delivered the last pass's writes, counted in the tests'
fake API servers:

| Pass | Before | After |
|---|---|---|
| The `Receiver` pass | 1 list | 0 |
| The `Deployment`'s `CECBus` and `Television` pass | 2 lists | 0 |
| The node workload's pass | 2 lists | 0 |
| Discovery, on each search | 1 list | 0 |
| A `Television` session event | 1 list | 0 |

A pass that runs before a watch delivers the process's own write reads
that object once, with a `GET`.

### The node workload watches the `Display`s of its machine

`display-operator`'s `Display` CRD declares `status.node` as a
selectable field. The node workload lists and watches the `Display`s
by the field selector `status.node=<its machine>`, so it receives no
other machine's `Display` writes. A `Display` that a bus names on
another machine is read from the API server. The `ClusterRole`
already grants `list` and `watch`, which a field-selected list needs.
The `CECBus` watch stays whole: a `CECBus` names its machines in a
list, which no field selector reaches.

An API server whose `Display` CRD declares no selectable field refuses
the selector. The node workload then starts with no `Display` watch,
tries the list again at each pass, and reads the named `Display` from
the API server on each pass until `display-operator`'s CRD with the
field is installed. Install that CRD first to skip that cost.

### No test searches the network

`TestMain` replaces the search for devices with a function that
panics, so a test that runs the loop without `noDiscovery(t)` or a
stub of its own fails, and no test sends SSDP or mDNS queries on the
local network. The tests of `serve` and of the loop stub it.

### The restart count

`equipment_watch_restarts_total` keeps its meaning. `watchCollection`
counts each watch the reflector opens after the first. The reflector's
first read is itself a watch when it reads with a streaming list.

### The backstops

Two timers in the `Deployment` run a pass with no event. Both stay,
and neither re-reads a state that a watch reports.

* `backstopInterval` in `reconcile.go`, 30 seconds, covers failures
  that no event follows: a list the API server refused, a
  `Television`'s `status.session` write it refused, a declared setting
  whose send failed, and a setting the receiver took and still reports
  at another value. The comment at the constant names each one. The
  port changes none of them: a refused write sends no watch event,
  and a receiver that does not confirm a setting sends nothing to the
  API server.
* `cecBusClock` in `cecbus_controller.go`, 30 seconds, is a clock. An
  adapter's entry goes stale `staleAfter` after its `reportedAt`, and
  a node workload whose pod dies writes nothing that wakes the loop.
  The same tick tries a refused status write again, and starts the
  watch of a `Television` or `Display` definition that was installed
  after the operator started. The comment at the variable names each
  one.

### The reflector and the organization's guards

The reflector does not meet the three guards of the `operators` skill
exactly. The skill lists the five differences that a reading of
client-go v0.36.3's `tools/cache/reflector.go` gives, and the
organization accepts them because none of them loses an event.

### The module

The module already had client-go v0.36.3 in its graph, as an indirect
requirement that only the tests linked. `crd_columns_test.go` and
`receiver_test.go` validate the CRDs with
`k8s.io/apiextensions-apiserver`'s validation package, which imports
`k8s.io/apiserver`'s webhook package, which imports
`k8s.io/client-go/rest`. The binary linked none of it. The port makes
client-go a direct requirement at the same version. liken's
`k3s/VERSION` names v1.36.4+k3s1, so the minor matches, and this plan
changes no pin.

The `ClusterRole` already grants `list` and `watch` on the four kinds,
and a streaming list uses the `watch` verb, so the RBAC does not
change.

## Measurements

The image is `FROM scratch` and holds one binary, so the stripped
binary is the image's size. Both builds used the `Dockerfile`'s flags,
`CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`, with Go 1.27.0.

| | Hand-written loop | client-go |
|---|---|---|
| Stripped binary | 10,981,536 bytes | 15,618,208 bytes |
| Linked Go packages (`go list -deps .`) | 265 | 397 |

The one binary serves both workloads, so the `cec` DaemonSet's pods
pay the same increase. The idle RSS of the watches is not measured
yet; it is part of the drill below.

In lines, the port removed 364 lines of code and 545 lines of tests,
and added 530 lines of code and 977 lines of tests. Most of the added
test lines are the scripted API server in `watchserver_test.go`, the
tests of the stores in `watchcache_test.go`, and the fake API server in
`cecapi_test.go`, which now answers a streaming list and sends each
object in its events.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The fake API servers in `cecapi_test.go` and
`reconcile_test.go` answer a streaming list the way the API server
does, and send real objects in their events. So every test that runs
a workload runs its watches through the real reflector. A watch that
resumes from an old version gets a `410`.

| What | Test |
|---|---|
| An object converts into the operator's struct, and one that does not convert is an error that names it | `TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt` |
| A status write does not wake a marked watch, and a spec edit, a move, a new UID, a new object, and a removed object do | `TestAMarkedWatchWakesOnlyWhenTheMarkMoves` |
| A removal whose tombstone holds no copy wakes the loop, and so does an object that does not convert | `TestAMarkedWatchWakesOnlyWhenTheMarkMoves` |
| Through the reflector: each watch wakes the loop when its first read is done, even an empty read | `TestEachWatchWakesTheLoopWhenItsFirstReadIsDone` |
| Through the reflector: a status write wakes the `Receiver` loop and not the spec watch, and a spec edit wakes the spec watch | `TestAReceiverEventWakesTheLoopThatReadsIt` |
| Through the reflector: an edit made while the watch was down wakes the loop, and a status write does not | `TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit` |
| Through the reflector: a watch that ends opens again, and the reopening is counted | `TestAWatchThatEndsIsOpenedAgainAndCounted` |
| The watches use the ServiceAccount's CA and token, and a missing CA ends the watch | `TestTheWatchesUseTheServiceAccount` |
| A `Receiver` from a watch converts to the same struct that a list decodes | `TestAReceiverFromTheWatchIsTheReceiverAListGives` |
| A store answers a pass only after its first read, while its watch runs, and when every object converts | `TestAStoreAnswersOnlyWhenItHoldsTheWholeCollection` |
| A `Display` is read from the store, or from the API server with no watch | `TestADisplayIsReadFromTheStore` |
| A pass reads every kind from the stores once the watches have read, and a settled pass sends no request | `TestTheCECBusPassReadsTheStores`, `TestTheNodePassReadsFromTheStores` |
| A pass does not act again on a copy older than its own write, and a read after its own write answers the write | `TestAPassDoesNotActOnACopyOlderThanItsOwnWrite`, `TestAReceiverReadAfterItsOwnStatusWriteAnswersTheWrite` |
| A refused read of a copy older than the operator's own write is an error | `TestARefusedReadOfACopyOlderThanItsOwnWriteIsAnError` |
| The node workload's `Display` store holds its own machine's `Display`s | `TestTheNodeWatchesTheDisplaysOfItsMachine` |
| The node workload starts when the API server refuses the `Display` selector | `TestTheNodeStartsWhenTheDisplayListIsRefused` |

## One operator holds the `Lease`

This part is not built. The organization's design gives each operator
that runs as a `Deployment` a `Lease` through client-go's
`tools/leaderelection`, so the `Deployment` can move from `Recreate`
to `RollingUpdate` and roll with no gap. Two facts about this operator
stop it.

1. **The same binary runs as a `DaemonSet`.** `deploy/cec.yaml` runs
   `equipment-operator cec` from the operator's own image on each node
   with a CEC adapter. `tools/leaderelection` imports the typed
   clientset, which adds about 23 MB of binary and about 12 MB of RSS.
   The organization accepts that cost for the operator process and not
   for a role that runs on each node. The node workload and the
   `Deployment` are one `main` package, so leader election cannot be
   left out of one role without a second binary.
2. **The `Deployment` uses the host network.** A pod on the host
   network holds its ports on the node, and this one listens on 9260
   for metrics. A second copy cannot start on the same node while the
   first runs, so a `RollingUpdate` with `maxSurge: 1` and
   `maxUnavailable: 0` places the new pod on another node, and on a
   one-node cluster the new pod waits in `Pending` and the rollout
   never finishes. A copy that waits for the `Lease` would also bind
   the port and could search for devices by mDNS and SSDP before it
   leads.

The options for the first fact:

* **A second `main` package.** Move the code that both workloads use
  into packages of their own, and build two commands. The change is
  large, because the root package holds both workloads.
* **A build tag.** Build the root package twice: once with leader
  election for the `Deployment`, and once without it for the node
  workload. The image carries both binaries. `go vet` and the tests
  cover one build, so CI needs a build of the other.
* **Leader election only in the `Deployment`, with no split,** accepts
  the typed clientset's cost on every node with an adapter. The
  organization's design refuses this.

When the election is built, it takes media-operator's release of the
`Lease` after a late renewal. Cancelling the election does not recall
a renewal already sent, so that renewal can land after the release
read the `Lease`, and the release then fails on a conflict. A copy
that waits for the `Lease` would then wait the whole lease duration.

The second fact needs a decision whatever the first answer is: keep
`Recreate` on the host network, or move the metrics listener and
discovery so that a waiting copy holds no host port.

## The drill still owed

On liken-1, with the operator on a build of this change:

1. Read the working set of the operator container and of one `cec`
   pod, and compare them with the build before this change.
2. Edit a `Receiver`'s `spec.volume` and check that the standing
   session takes the new step at once.
3. Move a `Display` to a new physical address and check that its
   `Television`'s `status.displays` follows at once.
