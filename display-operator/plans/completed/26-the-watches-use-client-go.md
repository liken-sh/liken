# The watches use client-go

Plan 26. Built on 2026-09-27. The numbers below come from a k3s API
server in Docker, not from liken-1. The drill that is still owed is at
the end of this plan.

The operator and display-api watched seven collections through one
loop written by hand in `objectwatch.go`. Each watch now runs on
client-go's reflector, in the lean form that the `operators` skill in
the organization's `.agents` repository records, and the hand-written
loop is gone.

## The problem

`objectwatch.go` held `watchList`, `watchNamed`, and `streamList`, and
`wakewatch.go` held `watchWakes` on top of them. The tests of the loop
alone were 900 lines. Seven other repositories in the organization
each had a loop of the same kind. Reviews in 2026-09 found the same
faults in several of them, and each fix landed in one repository at a
time.

On 2026-09-27 the organization chose client-go's reflector for every
operator. The reason is maintenance: upstream maintains and tests the
reflector, so the project stops owning eight loops. The reference port
is `bluetooth-operator`'s plan 09, and this port copies its shape.

## The watches

A search for every watch and every list-then-watch in the repository
found seven. Each one moves.

| Process | Collection | Scope | What a change does |
|---|---|---|---|
| operator | `Display` | the whole cluster | wakes the Display loop and the placement pass |
| operator | `Layout` | the whole cluster | wakes the placement pass |
| operator | `Pod` | `fieldSelector=spec.nodeName=<node>` | wakes the placement pass |
| display-api | `Pod` | `liken-system`, `labelSelector=app=display-operator` | moves one pod in the sidecar index |
| display-api | `Secret` `display-api-tls` | `fieldSelector=metadata.name=` | serves the owner's rotated leaf |
| display-api | `Secret` `display-capture-server` | `fieldSelector=metadata.name=` | mints the sidecar's leaf again when it is gone or no longer valid |
| display-api | `ConfigMap` `extension-apiserver-authentication` | `kube-system`, `fieldSelector=metadata.name=` | takes up a rotated client authority |

Three watches in the repository are not Kubernetes watches, and they
do not move: the Wayland output watch in `wayland.go`, the uevent
socket in `uevents.go`, and the inotify watches in `arrivals.go` and
`serving.go`. The CLI plugin in `cli/` opens no watch.

## The design

### What moved

`watch.go` holds `watchCollection`, which runs one watch on
`cache.NewInformerWithOptions` with the dynamic client. The program
imports only these parts of client-go for the watches:

* `k8s.io/client-go/tools/cache`, for the reflector and the informer.
* `k8s.io/client-go/dynamic`, which lists and watches any resource
  with no generated code.
* `k8s.io/client-go/rest`, for `rest.InClusterConfig`.

The operator's main package does not import `dynamicinformer`,
`informers`, or `kubernetes` (the typed clientset). The CLI plugin is
a separate binary, and it already linked the typed clientset for its
port-forward and its completion. That does not change.

A `collectionWatch` names the resource, the namespace, and the label
and field selectors. The informer's list and watch set the same
selectors, so the API server applies them and a change outside them
never reaches the process. A transform removes
`metadata.managedFields` from each object before the informer stores
it.

`convert` decodes an object into this program's own struct with
`runtime.DefaultUnstructuredConverter`, and unwraps the tombstone
(`cache.DeletedFinalStateUnknown`) that the informer sends for an
object deleted while the watch was down. An object that does not
convert is an error that names its kind and name, and the handler
logs it.

These parts are gone: `objectwatch.go`, `wakewatch.go`, `Client.Watch`,
the stream client without a request timeout that only `Client.Watch`
used, `ErrGone`, `displayWatchTimeout`, the sidecar index's `replace`,
and `PodList`'s resource version.

### What stayed

The program's own `Client` in `apiclient.go` sends every read and
write that a pass makes. Only the watching moved. Each watch keeps its
rule for what a change does. "The passes read the stores" below
changes both: the passes now read from the watches' stores, and the
`Display` watch wakes only on an edit.

* **The wake watches.** `watchWakes` wakes the pass on every add,
  update, and delete, and converts nothing, because the pass that
  follows reads every object again. The `Display` watch still wakes on
  every `Display` event in the cluster, the operator's own status
  writes included, as it did before. The pass writes a status only
  when it differs from the stored one, so a status write wakes one
  more pass that writes nothing. The first read wakes the pass once
  through the informer's `HasSyncedChecker`, even a read that finds
  no object. At a start, the pass can read the collection before the
  watch does, and a change made between the two reads is in the
  watch's read and in no event. A watch that resumes after a gap
  receives each change made during it. After a `410`, the informer
  reads the collection again and reports each object as an add, an
  update, or a delete, and each one wakes the pass.
* **The named watches.** `watchNamed` calls its callback with the
  object on each add and update, and with nil on a delete. An object
  that is absent from the first read has no event, so the watch
  reports nil once the first read is done. For the sidecar's `Secret`,
  that report is what mints the leaf again. A lock keeps that report
  and the handler's calls in order.
* **The sidecar index.** The handler adds and updates one pod by name
  and drops one on a delete. A deleted pod that does not convert, and
  a tombstone that holds no copy, drop the pod by the name in its key.
  After a `410`, the informer reports only the differences from what
  it held, so a pod that stayed never leaves the index on the way.
  The index's own `replace` gave the same guarantee for each listing.
* **`display_watch_restarts_total`.** Each watch that the API server
  accepts after the first one counts one restart. A refused watch is
  a retry and counts nothing. The reflector also opens a watch for the
  streaming list that follows a `410`, so that read counts as a
  restart too.

The RBAC does not change. A streaming list uses the `watch` verb, and
the fallback to a plain list uses `list`. The operator's `ClusterRole`
grants both on `displays`, `layouts`, and `pods`. display-api's `Role`
grants both on its pods and on its two `Secret` names, and the
built-in `extension-apiserver-authentication-reader` grants both on
the `ConfigMap`.

The module already required client-go v0.36.3 for the CLI plugin, so
the port adds no module requirement. `go mod tidy` added one indirect
line. liken's `k3s/VERSION` names Kubernetes v1.36, and the minor
matches. This plan changes no pin.

### No leader election

The operator runs as a DaemonSet, one pod on each node with a card,
so no two pods compete for the same objects. display-api is a
Deployment, but it is an API server, not a reconciler. Neither one
takes client-go's leader election, and neither links the typed
clientset that it needs.

### The reflector and the organization's guards

The reflector does not meet the three guards in the `operators` skill
exactly. The skill lists five differences: a backoff of 0.8 to 1.6
seconds after a `410`, a new read after a short watch with no event,
a watch that delivered an event never counts as short, the life of a
watch counts from the request, and the backoff resets after two
minutes with no failure. None of them loses an event, and the
organization accepts them.

The reflector reads each collection with a streaming list, a watch
with `sendInitialEvents=true`, and falls back to a plain list when the
API server refuses it. Its watches time out after a random 5 to 10
minutes, where the hand-written loop asked for 290 seconds.

### The passes read the stores

Built on 2026-09-27, after the port. The operator's passes read the
objects that a watch holds from the watch's store, not from the API
server (`objectcache.go`). Each store holds the same selection the
passes read:

* The `Display` store holds every `Display`. The Display controller
  reads each present panel's `Display` from it on every pass, and its
  sweep lists it. The placement pass reads each screen's `Display` and
  lists the `Display`s when the compositor is dark. One `displayStore`
  serves both passes.
* The `Layout` store holds every `Layout`. Once it holds its first
  read, a name it does not hold is a `Layout` that does not exist, and
  the pass reads nothing from the API server for it.
* The pod store holds this node's pods. The placement pass reads the
  holders of a claim from it, and the compositor's restart count reads
  the operator's own pod from it. A holder on another node is read from
  the API server.

A list comes from a store only after the store holds its first read.

A `Display`'s copy in the store can be older than the operator's own
last write. The Display controller acts on the hardware from its
status: a captured value, a mode the compositor declined, a write it
made. A test showed a pass from an older copy capture the dark panel's
brightness over the saved one. So the `displayStore` remembers the
`resourceVersion` of each `Display`'s newest copy that the operator
wrote or read from the API server, and reads a `Display` from the API
server when the store's copy has another version. Both passes write
`Display` status on their own goroutines, so the `displayStore` sends
one `Display` request to the API server at a time, and the memo notes
the versions in the order the API server answered them. A read that
the store answers takes only the memo's lock, so it never waits on the
other pass's request.

A write from a copy that another writer changed since gets `409
Conflict`:

* The placement pass's report and its dark report read the `Display`
  again, compose their fields onto the fresh status, and write once
  more. The dark report leaves a `Display` whose fresh copy names
  another node.
* The Display controller's sweep does the same, and leaves a `Display`
  whose monitor moved to another node since the copy.
* The Display controller's pass over one panel wakes the next pass,
  which reads the card and the `Display` again. The fresh status lacks
  the records the refused write held, so the next pass can actuate
  again what this one did. The prepare path's budget of compositor
  restarts bounds a mode switch that repeats. This matches what a
  refused write did before the stores, when the next wake ran the
  pass.

These reads stay on the API server, each for a reason:

* The `ResourceSlice`, once per pass that publishes. A watch of one
  slice needs `list` and `watch` on every slice in the cluster, because
  RBAC cannot name one node's slice.
* A `ResourceClaim`, once per claim per placement pass. A claim has no
  field that selects a node, so a watch would send every node's
  operator every claim in the cluster, for the few claims on its
  screens.
* The `Display`s that seed the link history and the declare
  container's resting modes, once at the start. display-api reads a
  `Display` for each request, and opens no `Display` watch.

### The Display watch wakes on an edit

The `Display` watch woke both passes on every `Display` event in the
cluster, the status writes of every node's operator included. A search
of the code for what a pass reads from another writer's status found
one field it acts on: the placement pass reports a dark screen on each
`Display` whose `status.node` names this node, so a `Display` that the
Display controller adopts must wake it. The rest of the status is this
node's own record. So the watch now wakes the passes as the
`bluetooth-operator` edit watch does, on a new `Display`, a removed
one, and an update that changes `metadata.generation`, the deletion
mark, or `metadata.uid`, and also on an update that changes
`status.node`. That field changes only when a monitor moves. The
poll's tick of 10 seconds and the hardware's events still wake the
Display controller.

Requests to the API server, counted in the tests' fixtures
(`TestADisplayPassFromTheStoreSendsNoRead`,
`TestAPlacementPassFromTheStoresReadsOnlyTheClaim`,
`TestTheRestartCountReadsThePodFromTheStore`):

| Pass | Before | After |
|---|---|---|
| Display controller, settled, one panel | 1 `GET` | 0 |
| Display controller, a pass that sweeps | 1 `GET`, 1 `LIST` | 0 |
| Placement, one screen, one claim, a named `Layout` | 3 `GET`s, 1 `LIST` | 1 `GET` (the claim) |
| Placement, compositor dark | 1 `LIST` | 0 |
| Slice publish | 2 `GET`s (the slice, this pod) | 1 `GET` (the slice) |

The Display controller runs at least once every 10 seconds on each
node, so the settled pass alone was 6 reads a minute for each panel.
Each status write also woke both passes on every node in the cluster.
A write from a copy that another writer changed costs a refused write,
a `GET`, and a second write. The pass after the operator's own write
reads that `Display` from the API server once, if the watch has not
delivered the write yet. The stripped binary grew from 20,213,920 to
20,250,784 bytes, and the linked package count stayed at 472. The pass
model does not change.

## Measurements

The binaries were built with `CGO_ENABLED=0 go build -trimpath
-ldflags "-s -w"`, on the laptop, before and after the port.

| Binary | | Before | After |
|---|---|---|---|
| `display-operator` | stripped size | 15,437,984 bytes | 20,185,248 bytes |
| `display-operator` | linked Go packages | 354 | 472 |
| `kubectl-liken-display` (the CLI) | stripped size | 28,246,176 bytes | 28,246,176 bytes |
| `kubectl-liken-display` (the CLI) | linked Go packages | 701 | 701 |

The idle RSS came from a harness that linked every package of the
operator and ran only its three watches: `Display`, `Layout`, and the
pods on a node that ran none. It ran against a k3s v1.36.3-k3s1 API
server in Docker with the `Display` and `Layout` CRDs applied. The RSS
is `VmRSS` from `/proc/<pid>/status` 45 seconds after the start, in two
runs of each build.

| | Hand-written loop | client-go |
|---|---|---|
| RSS, idle, 50 `Display`s | 7,512 and 7,580 kB | 7,584 and 7,632 kB |
| RSS, idle, 250 `Display`s | 7,548 and 7,628 kB | 7,712 and 7,752 kB |

In both builds, the watches woke the pass once for each `Display` and
once for each first read: 53 wakes at 50 objects, and 253 at 250.
`bluetooth-operator`'s plan 09 measured 5 to 7 MB more RSS for its
client-go build. This harness did not show that difference, and the
reason is not known. The working set on liken-1 is the number that
counts, and the drill below reads it.

The port removed 452 lines of code and 1,083 lines of tests, and added
386 lines of code and 661 lines of tests.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The tests that remain run this program's handlers
through the real reflector, against `objectStore` in
`watchserver_test.go`. The store holds one collection and answers a
get, a list, a streaming list, a watch from a version, a create, and
an update, and it records the query of each list and watch.

| What | Test |
|---|---|
| A named watch sees each change to its object, and selects it by name | `TestTheWatchSeesEachChangeToTheObject` |
| A named watch reports an absent object as gone | `TestTheWatchSeesAnAbsentObjectAsGone` |
| An object or a tombstone that does not convert is an error that names it | `TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt` |
| Each wake watch wakes on its first read, a new object, an edit, and a removal, and selects only what its pass reads | `TestAWakeWatchWakesOnItsFirstReadAndOnEveryChange` |
| A watch that the API server ends counts one restart | `TestAReopenedWakeWatchCountsARestart` |
| A refused watch counts no restart | `TestARefusedWatchCountsNoRestart` |
| The sidecar index follows the watch, and the watch selects the sidecar pods by label | `TestTheIndexFollowsTheWatch` |
| A deletion drops its pod: a tombstone with a copy or with none, and a pod that does not convert | `TestADeletionDropsThePod` |
| The `Layout` and pod watches end with the context | `TestTheLayoutWatchEndsWithTheContext`, `TestThePodWatchEndsWithTheContext` |
| The client authority, the owner's leaf, and the sidecar's leaf follow their watches | `TestTheWatchTakesUpANewAuthority`, `TestTheWatchKeepsTheAnchorsWhenTheConfigMapGoes`, `TestTheAPITakesUpALeafTheOwnerRotated`, `TestTheAPIMintsTheSidecarsSecretAgainWhenItGoes` |

## Considered and set aside

* **`dynamicinformer`.** It links the typed clientset and an informer
  for every built-in kind, and this binary runs in the operator pod on
  every node with a card.
* **A work queue for each object** (`util/workqueue`). The operator
  runs one full pass for each wake, and the sidecar index and the
  named watches each act on one object at a time, so a handler is
  enough.
* **A filter on the `Display` watch for the operator's own status
  writes**, as `bluetooth-operator` compares the generation. It would
  change what wakes a pass, and this plan moves the watches without a
  change in behavior. It stays a separate decision.

## The drill still owed

On liken-1, with the operator and display-api on a build of this
change:

1. Read the operator container's working set on a node with a card,
   and compare it with the build before this change.
2. Change a pod's label that a `Layout` region selects, and check that
   the placement pass restacks the screen in the settle window.
3. Delete the `display-capture-server` `Secret`, and check that
   display-api mints it again at once.
