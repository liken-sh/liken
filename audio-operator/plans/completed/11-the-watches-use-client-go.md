# 11, The watches use client-go

Plan 11. Built on 2026-09-27. The numbers below come from a k3s API
server in Docker, not from liken-1. The drill that is still owed is at
the end of this plan.

Every Kubernetes watch in this repository now runs on client-go's
reflector, and the watch loop written by hand in `apiwatch.go` is
gone. The pass reads this machine's `Sink`s and `Source`s from the
watches' stores, so a settled pass sends the API server no read. The
operator's own `Client` still sends every write and every read that no
store answers.

## The problem

The operator and `audio-api` followed five collections through one
loop written by hand, `objectWatch` in `apiwatch.go`, with 441 lines
of tests for the loop alone. Seven other repositories in the
organization each had a loop of the same kind. Reviews in 2026-09
found the same faults in several of them, and each fix landed in one
repository at a time. This loop took three commits of its own to meet
the three guards in the `operators` skill.

On 2026-09-27 the organization chose client-go's reflector for every
operator, in the lean form that the `operators` skill records. The
reason is maintenance: upstream maintains and tests the reflector, so
the project stops owning eight loops. `bluetooth-operator` plan 09 is
the reference port, and this port copies its shape.

## The design

### The five watches

A search for every Kubernetes watch in the repository found five, in
two roles of the one binary. Each keeps the scope it had.

| Role | Collection | Selector | What a change does |
|---|---|---|---|
| operator (`DaemonSet`) | `Sink` | `status.node=<machine>` | wakes the pass on an edit |
| operator (`DaemonSet`) | `Source` | `status.node=<machine>` | wakes the pass on an edit |
| `audio-api` (`Deployment`) | `Secret` `audio-capture-server` in the operator's namespace | `metadata.name=audio-capture-server` | runs the capture leaf check |
| `audio-api` (`Deployment`) | `ConfigMap` `extension-apiserver-authentication` in `kube-system` | `metadata.name=extension-apiserver-authentication` | loads the cluster's client authority from the copy the watch delivers |
| `audio-api` (`Deployment`) | `Pod`s in the operator's namespace | `app=audio-operator` | updates the index of which pod is on each node |

The `Sink` and `Source` watches select by the selectable field
`status.node`, so each machine's operator receives only its own
machine's resources. The two named watches select by
`metadata.name`, which is what lets the `Role` in `deploy/api.yaml`
and the built-in `extension-apiserver-authentication-reader` grant
`list` and `watch` on one object. A streaming list is a `watch` with
`sendInitialEvents=true`, and it carries the same selector, so the RBAC
does not change.

The other event sources do not move: `pw-dump -m`, the cards' control
devices, the jack and ELD events, bluetoothd over D-Bus, and the inotify
watch on the capture container's `Secret` volume.

Neither role gets leader election. The operator is a `DaemonSet`, with
one pod on each machine. `audio-api` is an API server, not a
reconciler. Its one shared write is the domain's CA, and a create that
loses the race reads the winner's copy.

### What moved

`watch.go` holds `collectionWatch`, which runs one watch on client-go's
informer (`cache.NewInformerWithOptions`) with the dynamic client. The
binary imports only these parts of client-go:

* `k8s.io/client-go/tools/cache`, for the reflector and the informer.
* `k8s.io/client-go/dynamic`, which lists and watches any resource with
  no generated code.
* `k8s.io/client-go/rest`, for `rest.InClusterConfig`.

It does not import `dynamicinformer`, `informers`, or `kubernetes`
(the typed clientset). `go list -deps` of the main package confirms
it. The `liken-audio` CLI plugin in `cli/` already linked client-go
for `kubectl`'s configuration and port-forward. That is a separate
binary, and this port does not change it.

A handler that reads the object decodes it into the operator's own
struct with `runtime.DefaultUnstructuredConverter`: `Sink`, `Source`,
and `pod`. An object that does not convert is an error that names the
object, and the handler logs it. A transform removes
`metadata.managedFields` from each object before the informer stores
it.

These parts are gone: `objectWatch` and its constants, `objectKeeper`,
`podKeeper`, `ErrGone`, `Client.Watch`, and the stream client with no
request timeout that only `Client.Watch` used. The metric
`audio_watch_restarts_total` stays. It counts each watch the API
server accepted after the first one it accepted. A refused watch is
not counted, so an outage adds nothing, and the plain list's watch that
follows a refused streaming list is not a restart.

### What each watch wakes

* **`Sink` and `Source`.** An update wakes the pass only when
  `metadata.generation`, `metadata.deletionTimestamp`, or
  `metadata.uid` changed. A write to the status subresource changes
  none of them, so the operator's own status write no longer wakes a
  pass. The hand-written loop woke the pass for every event, so each
  status write cost one more pass, which read every endpoint again and
  wrote nothing. A resource that enters the selection arrives as an
  add, and one that leaves it, such as a Bluetooth speaker that moved
  to another machine, arrives as a delete. Both wake the pass. Each
  watch also wakes the pass once when its first read is done, because
  the first pass can read the resources before the watch does.
* **The capture `Secret` and the client authority `ConfigMap`.** Every
  add, update, and delete runs the check. An object that does not
  exist arrives as no event, so when the first read holds no such
  object, the check runs once at its end, and for the `Secret` it
  mints the capture leaf. A lock runs one check at a time for each
  object, because the informer calls the handler and the end of the
  first read on two goroutines.
* **The operator's pods.** The index held one pod for each node and
  chose between two pods on a node when an event arrived. The informer
  hands a delete by the pod's key, and a tombstone after a gap can hold
  no copy of the pod, so the index now holds every pod by name and
  chooses when a request asks for a node. The choice is the same: a
  pod that is not leaving takes the place of one that is, and of two
  in the same state, the newer one answers. The pod that the index
  answers for a node no longer depends on the order of the events.

### The pass reads the stores

The `Sink` and `Source` stores hold every resource whose `status.node`
is this machine, which is the same selection the pass listed. The
pass now reads from them (`endpointcache.go`):

* Each endpoint's `Sink` or `Source` comes from the store. A resource
  the store does not hold is read from the API server: a new endpoint
  whose resource does not exist yet, a resource with no `status.node`,
  and a Bluetooth speaker's `Sink` whose `status.node` still names the
  machine it moved from.
* The sweep lists from the stores once both hold their first read,
  and from the API server before that. A store that holds part of its
  first read would leave a resource out of the sweep, and the sweep
  runs again only when the endpoints change or once per backstop
  interval.
* A copy in the store can be older than the operator's own last status
  write, because the watch delivers the write a moment after the API
  server answers it. A write from that copy carries an older
  `resourceVersion`, and the API server answers `409 Conflict`. The
  write then reads the resource from the API server, composes the
  status again from what it holds, and writes once more if the status
  still differs. The sweep composes nothing for a resource whose fresh
  copy names another machine in `status.node`, such as a Bluetooth
  speaker's `Sink` that moved, so it never writes the absence onto
  another machine's resource.
* A copy of a resource somebody deleted answers the status write with
  `404`, and the pass creates the resource again. A pass with no
  status to write sends nothing, and the delete's own event takes the
  copy out of the store and wakes the pass that creates it. The fixture in `sinks_test.go` now answers a `PUT`
  from an older version with `409`, the way the API server does.

These reads stay on the API server, each for a reason:

* `audio-api` reads the capture `Secret` on each change, because the
  check writes the `Secret`, and a check that read the watch's copy
  before the watch delivered that write would mint a second leaf.
* `audio-api` reads the client authority `ConfigMap` once at the
  start, before the listener opens, because the first handshake needs
  the authority and the watch has not read it yet. After that, the pool
  takes the copy each event delivers, and a rotation costs no read.
* `audio-api` reads a `Sink` or a `Source` by name for each tap. It
  does not watch them: its `ClusterRole` grants `get` alone, and a tap
  names one endpoint.
* The operator reads its node once at the start, and its own
  `ResourceSlice` and each `ResourceClaim` by name. No watch holds
  them, and a watch of `ResourceClaim`s would take every claim in the
  cluster.

Requests to the API server for one pass, counted in the test's fixture
(`TestAPassFromTheStoresSendsNoRead`):

| Pass | Before | After |
|---|---|---|
| Settled, two card endpoints and one speaker | 3 `GET`s | 0 |
| The speaker left, so the pass sweeps; two card endpoints | 2 `GET`s and 2 `LIST`s | 0 |

A pass that writes a status from a current copy sends one `PUT`, where
it sent a `GET` and a `PUT`. A pass that runs before the watch
delivered its own last write works from an older copy, and for each
status it writes it sends a `PUT` that is refused with `409`, a `GET`,
and a second `PUT`.

### The reconcile loop

The pass model stays: every event source wakes one channel, a settle
window merges a burst of wakes, and each wake runs one full pass over
every endpoint. A work queue for each object
(`util/workqueue`) would not make the code simpler: a pass reads the
card, PipeWire's graph, and bluetoothd's paired set once for all
endpoints, and most wakes come from those sources, not from the API
server. A failed pass keeps its bound: the next wake or the backstop
tick of 60 seconds runs the next pass, and the comment at
`backstopInterval` names the failures the tick covers.

### The reflector and the organization's guards

The reflector does not meet the three guards in the `operators` skill
exactly. `bluetooth-operator` plan 09 lists the five differences, read
from client-go v0.36.3's `tools/cache/reflector.go`. None of them loses
an event, and the organization accepts them in return for a loop it
does not maintain.

The module already required client-go v0.36.3 for the CLI plugin, so
the port adds no module requirement. `go mod tidy` added one indirect
line, `github.com/pmezard/go-difflib`. This plan changes no pin.

## Measurements

Each binary ran only the five watches, on the laptop, against a k3s
v1.36.3-k3s1 API server in Docker, with the `Sink` and `Source` CRDs
from `deploy/crds.yaml`. A file added to a copy of each tree started
the watches from `init`, so the binary linked every package of the
operator. The `Sink`s all carried `status.node=node-1`, the machine the
watches selected. The RSS is `VmRSS` from `/proc/<pid>/status` 45
seconds after the start, in two runs of each build.

| | Hand-written loop | client-go |
|---|---|---|
| Stripped binary | 15,401,120 bytes | 20,275,360 bytes |
| Binary as the image builds it, not stripped | 22,528,102 bytes | 29,591,862 bytes |
| Linked Go packages, main package | 353 | 473 |
| RSS, idle, 50 `Sink`s | 17.1 to 18.4 MB | 24.3 to 24.4 MB |
| RSS, idle, 250 `Sink`s | 19.9 to 20.1 MB | 25.7 MB |

The binary and RSS numbers were measured before the pass read from the
stores. That change reads stores the watches already held, so it adds
no memory, and the stripped binary grew by 16 KB more, to 20,291,744
bytes. The RSS was not measured again. The image is `FROM scratch` and
copies the binary as the build writes it, so the image grows by about
7 MB. The operator runs on
every machine, so each machine pays the 5.6 to 7 MB of RSS.

Against the same server, a build that printed each wake showed the
rules on a real API server. The two watches' first reads woke the pass
252 times: 250 adds and one sync for each watch. A status write did not
wake it, a spec edit did, a label change did not, a `Sink` whose
`status.node` moved to another machine did, and a deletion did.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The tests that remain run this binary's handlers,
most of them through the real reflector against a scripted API server
that answers a streaming list the way the API server does.

| What | Test |
|---|---|
| An object that does not convert is an error that names it | `TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt` |
| The informer stores no `managedFields` | `TestTheInformerStoresNoManagedFields` |
| Through the reflector: a watch opened again counts one restart | `TestAWatchThatReopensCountsOneRestart` |
| A status write does not wake the pass, and an edit, a deletion request, a new object, a removed object, and a tombstone with no copy do | `TestAChangeWakesTheLoopOnlyForAnEdit` |
| Through the reflector: both collections select this machine's resources | `TestTheWatchSelectsThisMachinesResourcesInBothCollections` |
| Through the reflector: the first reads wake the pass, a status write does not, and a spec edit does | `TestASpecEditWakesTheLoopAndAStatusWriteDoesNot` |
| Through the reflector: after a `410`, a spec edit made in the gap wakes the pass and a status write does not | `TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit` |
| Through the reflector: a `Sink` that enters or leaves the selection wakes the pass | `TestASinkThatEntersOrLeavesTheSelectionWakesTheLoop` |
| Through the reflector: a deleted capture `Secret` is minted again, and both named watches select by name | `TestADeletedCaptureSecretIsMintedAgainWhenTheWatchReportsIt` |
| Through the reflector: an absent capture `Secret` is minted at the end of the first read | `TestAnAbsentCaptureSecretIsMintedWhenTheFirstReadIsDone` |
| Through the reflector: a change to the client authority `ConfigMap` loads the authority from the event, with no read | `TestARotatedClientAuthorityIsLoadedWhenTheWatchReportsIt` |
| A client authority `ConfigMap` that does not exist keeps the pool | `TestAnAbsentClientAuthorityKeepsThePool` |
| A pass from the stores sends no read, settled or sweeping | `TestAPassFromTheStoresSendsNoRead` |
| A sweep's status write from an older copy reads the resource again and lands | `TestAStatusWriteFromAnOlderCopyReadsAgainAndLands` |
| A reconcile's status write from an older copy reads the resource again and lands | `TestAReconcileFromAnOlderCopyReadsAgainAndLands` |
| The sweep leaves a `Sink` that another machine took since the store's copy | `TestTheSweepLeavesASinkAnotherMachineTook` |
| A `Sink` deleted since the store's copy is created again | `TestASinkDeletedSinceTheStoresCopyIsCreatedAgain` |
| Through the reflector: the pod watch selects by label, and adds, changes, and deletes move the index | `TestThePodWatchFollowsThePodsIntoTheIndex` |
| Through the reflector: a pod gone from the read after a `410` leaves the index | `TestAPodGoneFromANewReadLeavesTheIndex` |
| A pod removed while the watch was down is forgotten, with or without a copy | `TestAPodRemovedWhileTheWatchWasDownIsForgotten` |
| A pod that does not convert leaves the index as it was | `TestAPodThatDoesNotConvertLeavesTheIndexAsItWas` |

The index tests from before the port stay, and now call `put` and
`forget`.

## Considered and set aside

* **`dynamicinformer`.** It links the typed clientset and the
  informers for every built-in kind. `bluetooth-operator` plan 09
  records a 39.1 MB binary with it.
* **A work queue for each object** (`util/workqueue`). The operator
  runs one full pass for each wake, and `audio-api` runs one check for
  each change, so a handler that calls them is enough.
* **Waking the pass on every `Sink` event**, as the hand-written loop
  did. A status write changes nothing the pass reads, so the pass it
  woke read every endpoint again and wrote nothing.

## The drill still owed

On liken-1, with the operator and `audio-api` on a build of this
change:

1. Read the operator container's working set on a 1 GB machine, and
   compare it with the build before this change.
2. Edit a `Sink`'s `spec.volume` and time how long the card's level
   takes to change.
3. Delete the `Secret` `audio-capture-server` and time how long
   `audio-api` takes to write it again.
4. Roll the operator's `DaemonSet`, and tap a `Sink` on each machine
   during the roll, to check that the pod index follows the new pods.
