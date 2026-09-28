# 09, The watches use client-go

Built on 2026-09-27. The numbers below come from a k3s API server in
Docker, not from liken-1. The drill that is still owed is at the end
of this plan.

## The problem

The operator watched its three kinds, `PairingRequest`, `Adapter`, and
`Peripheral`, through one watch loop written by hand in `watch.go`:
`listThenWatch`, `streamChanges`, and `pauseWatch`, with 701 lines of
tests for the loop alone. Seven other repositories in the organization
each had a loop of the same kind. Reviews in 2026-09 found the same
faults in several of them, and each fix landed in one repository at a
time.

On 2026-09-27 the organization chose client-go's reflector for every
operator, in the lean form that the `operators` skill in `.agents`
records. The reason is maintenance: upstream maintains and tests the
reflector, so the project stops owning eight loops. This operator
moves first, and its watches are the reference that the other
operators copy.

## The design

### What moved

`watch.go` now holds `watchCollection`, which runs one watch on
client-go's informer (`cache.NewInformerWithOptions`) with the dynamic
client. The operator imports only these parts of client-go:

* `k8s.io/client-go/tools/cache`, for the reflector and the informer.
* `k8s.io/client-go/dynamic`, which lists and watches a custom
  resource with no generated code.
* `k8s.io/client-go/rest`, for `rest.InClusterConfig`.

It does not import `dynamicinformer`, `informers`, or `kubernetes`
(the typed clientset). Those packages link a client and an informer
for every built-in kind, and the operator watches none of them.

The informer hands each handler an `*unstructured.Unstructured`.
`convert` decodes it into the operator's own struct with
`runtime.DefaultUnstructuredConverter`, and unwraps the tombstone
(`cache.DeletedFinalStateUnknown`) that the informer sends for an
object deleted while the watch was down. An object that does not
convert is an error that names the object, and the handler logs it.

A transform removes `metadata.managedFields` from each object before
the informer stores it. The operator never reads the field, and the
transform lowers the idle RSS at 250 `PairingRequest`s from 28.2 MB to
25.5 MB (see the numbers below).

These parts of the loop are gone: `listThenWatch`, `streamChanges`,
`pauseWatch`, the error values and timing constants they used,
`Client.Watch`, and the stream client without a request timeout that
only `Client.Watch` used. `watch_faults_test.go` is gone, and
`watch_test.go` no longer tests the loop. In all, the port removed
309 lines of code and 682 lines of tests, and added 264 lines of code
and 326 lines of tests.

### What stayed

The operator's own `Client` in `apiclient.go` sends every read and
write that a pass makes. Only the watching moved.

Each watch keeps the rule for when it wakes the loop:

* **`PairingRequest`.** `requestWatcher` holds the requests, and wakes
  the loop when a request is unfinished or its TTL is up. Its clock,
  set to the earliest TTL still ahead, is unchanged.
* **`Adapter` and `Peripheral`.** An update wakes the loop only when
  the generation or the deletion mark changed, so the operator's own
  status writes do not wake a pass. A new object and a removed object
  wake it. The `Adapter` watch ignores an `Adapter` whose
  `status.node` names another node, unless it is the `Adapter` for the
  radio this pod holds.
* **The `Peripheral` watch follows the radio.** It selects the
  adapter's label. A label selector is fixed for the life of an
  informer, so when the pass reports a new address, `followPeripherals`
  stops the informer for the old address, waits until it has returned,
  and starts a new one.

Two parts of the edit watcher changed shape, and neither changed what
wakes the loop:

* The edit watcher kept its own copy of each object's mark, to tell a
  status write from an edit. The informer hands an update both the
  copy it held and the new copy, so the handler compares their marks
  and keeps no copy. The mark now holds the UID as well. After a gap,
  an object that somebody deleted and created again with the same
  name reaches the handler as an update, and its generation can be the
  same as the old object's.
* The edit watcher woke the loop after every list, because it could
  not tell which edits a gap in the watch held. After a gap, the
  informer reads the collection again and reports each difference from
  what it held as an addition, an update, or a deletion, so an edit
  made in the gap reaches the handler as an event. The watcher still
  wakes the loop once when an informer's first read is done, through
  the informer's `HasSyncedChecker`. At a start, the pass can read the
  objects before the watch does, and an edit made between the two
  reads is in the watch's read and in no event.

The backstop tick stays at 60 seconds, for the two failures plan 08
names. The ClusterRole already grants `list` and `watch` on the three
kinds, and a streaming list uses the `watch` verb, so the RBAC does
not change.

The module already required client-go v0.36.3 for the CLI plugin, so
the port adds no module requirement. `go mod tidy` added one indirect
line. liken's `k3s/VERSION` names v1.36.4+k3s1. The minor matches;
this plan changes no pin.

### The reflector and the organization's guards

The reflector does not meet the three guards in the `operators` skill
exactly. A reading of client-go v0.36.3's `tools/cache/reflector.go`
gives five differences:

1. After a `410`, it waits a backoff of 0.8 to 1.6 seconds before it
   reads the collection again. The guard reads again at once.
2. A watch that closes in under a second with no event makes it read
   the collection again after the backoff. It does not resume from the
   last version.
3. A watch that delivered an event is never short, however soon it
   closed.
4. It measures a watch's life from the request, not from the `200`.
5. Its backoff resets after two minutes with no failure, not after one
   watch that ran for a second.

None of these loses an event. Differences 1 and 2 cost one more read of
the collection, or a read a second later. Differences 3, 4, and 5 change
only how long the reflector waits during a fault. The organization
accepts them in return for a loop it does not maintain.

The reflector also reads a collection with a streaming list: a watch
with `sendInitialEvents=true` that sends each object as an `ADDED`
event and ends the initial events with a bookmark. The
`WatchListClient` feature is on by default in v0.36. The reflector
falls back to a plain list when the API server refuses it.

### The pass reads the stores

Built on 2026-09-27, after the port. The pass reads the objects that a
watch holds from the watch's store, not from the API server
(`objectcache.go`). Each store holds the same selection the pass
listed:

* The `Adapter` store holds every `Adapter`. `ensureAdapter` reads the
  radio's `Adapter` from it, and `releaseDepartedAdapters` lists it.
* The `Peripheral` store holds one radio's `Peripheral`s. The store
  answers only for the radio the watch follows, so the first pass for a
  new radio lists from the API server.
* The `PairingRequest` store holds every request.
* A new watch holds this radio's bond `Secret`s, in the pod's namespace
  with the adapter label. It wakes nothing: `persist` reads it in place
  of one `GET` for each bond on every pass. The `Role` gains `watch` on
  `secrets` for it. The watch starts when the bond store learns the
  radio, which is then fixed for the life of the process.

A list comes from a store only after the store holds its first read.
An object a store does not hold is read from the API server.

A copy in a store can be older than the operator's own last write,
because the watch delivers the write a moment after the API server
answers it, and later while the watch is down. A pass that acted on such
a copy would act again on a change it made: a test showed a pass open
the pairing window again for a request it had paired. So the operator
remembers the `resourceVersion` of each object's newest copy that it
wrote or read from the API server (`versionMemo`), and reads an object
from the API server when the store's copy has another version. The
versions are compared only for equality.

A copy that another writer changed since gets `409 Conflict` on a
write, and the write reads the object again and writes once more if the
fresh copy still needs it:

* An `Adapter`'s finalizer patch and status write settle again from
  the fresh copy. The release of a departed `Adapter` reads it again
  and releases it only if the fresh copy still names this node.
* A `Peripheral`'s status is composed again from the fresh copy. The
  disconnect counter and the lost-bond line come from the copy that a
  landed write replaced, so an older copy never counts a drop twice.
* A `PairingRequest`'s status cannot be composed again, because the pass
  pairs and opens the radio's window while it composes. When the fresh
  copy holds the status the pass composed from, a spec edit made the
  conflict, and the status lands on the fresh copy. When it holds a
  newer status, the pass writes nothing and the follow-up pass composes
  from it.
* A bond `Secret` is compared again with the fresh copy, and written
  once more when it still differs.

These reads stay on the API server, each for a reason:

* The `ResourceSlice`, once per pass. A watch of one slice needs `list`
  and `watch` on every slice in the cluster, because RBAC cannot name
  one node's slice, and the `ClusterRole` keeps other drivers'
  inventories out of reach.
* A deleting `Peripheral`, once per pass of its teardown. The store can
  still hold a `Peripheral` whose finalizer this operator released, and
  a teardown from that copy would retire a device that no `Peripheral`
  names.
* The owner `Adapter` of a new `Peripheral`, once per creation. The
  store can hold an `Adapter` that somebody deleted and the operator
  created again, and an owner reference to its old UID would let
  garbage collection take the new `Peripheral`.
* The node, the older per-adapter `Secret`, and the list of bond
  `Secret`s that `restore` makes, once each at the start.

Requests to the API server for one pass, counted in the tests'
fixtures (`TestAPassFromTheStoresSendsNoRead`,
`TestPersistFromTheStoreSendsNoRead`), for a settled pass with N bonds:

| Reads | Before | After |
|---|---|---|
| `Adapter`s | 1 `LIST`, 1 `GET` | 0 |
| `Peripheral`s | 1 `LIST` | 0 |
| `PairingRequest`s | 1 `LIST` | 0 |
| bond `Secret`s | N `GET`s | 0 |
| `ResourceSlice` | 1 `GET` | 1 `GET` |
| Total | 5 + N | 1 |

A write from a current copy costs what it did. A write from an older
copy that another writer changed costs a refused write, a `GET`, and a
second write. The pass after the operator's own write reads each object
it wrote from the API server once, if the watch has not delivered the
write yet. The stripped binary grew from 19,964,064 to 20,013,216
bytes, and the linked package count stayed at 474. The pass model does not change: one full pass for
each wake, through the settle window.

## Measurements

Each binary ran only its watches, on the laptop, against a k3s
v1.36.3-k3s1 API server in Docker. The binary linked every package of
the operator, so every package initialized. All three watches ran, with
the `Peripheral` watch on one radio's label, and no `Adapter` or
`Peripheral` objects existed. The RSS is `VmRSS` from
`/proc/<pid>/status` 45 seconds after the start, in two runs of each
build.

| | Hand-written loop | client-go |
|---|---|---|
| Stripped binary (the image is `FROM scratch`, so the same) | 14,975,136 bytes | 19,927,200 bytes |
| Linked Go packages | 354 | 474 |
| RSS, idle, 50 `PairingRequest`s | 16.0 to 16.9 MB | 21.0 to 22.4 MB |
| RSS, idle, 250 `PairingRequest`s | 18.4 MB | 25.5 MB |

Without the `managedFields` transform, the client-go build measured
23.3 MB at 50 objects and 28.2 MB at 250.

Against the same server, a build that printed each wake showed the
rules on a real API server: a new `Peripheral` woke the loop, a label
change that did not raise the generation did not, and the deletion
woke it.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The tests that remain run this operator's handlers,
and most of them run the handlers through the real reflector against a
scripted API server. The server answers a streaming list the way the
API server does.

| What | Test |
|---|---|
| An object that does not convert is an error that names it | `TestAnObjectThatDoesNotConvertIsAnErrorThatNamesIt` |
| A status write does not wake the loop, and an edit, a deletion, a new object, and a removed object do | `TestAChangeWakesTheLoopOnlyForAnEdit` |
| A removal whose tombstone holds no copy of the object wakes the loop, and an object created again with the same name does too | `TestAChangeWakesTheLoopOnlyForAnEdit` |
| A deleted request whose tombstone holds no copy is forgotten | `TestARequestDeletedWithNoCopyIsForgotten` |
| Through the reflector: each watch wakes the loop when its first read is done, even an empty read | `TestAWatchWakesTheLoopWhenItsFirstReadIsDone` |
| An `Adapter` that another node holds does not wake the loop | `TestAnAdapterEditWakesTheLoopOnlyOnItsNode` |
| Through the reflector: a spec edit wakes the loop, and the status write before it does not | `TestAPeripheralEditWakesTheLoopAndAStatusWriteDoesNot` |
| Through the reflector: an edit made while the watch was down wakes the loop, and a status write does not | `TestAChangeWhileTheWatchWasDownWakesTheLoopOnlyForAnEdit` |
| Through the reflector: the `Peripheral` watch moves to a new radio's label | `TestThePeripheralWatchFollowsTheRadio` |
| Through the reflector: a new `PairingRequest` wakes the loop | `TestANewRequestWakesTheLoopAtOnce` |
| Through the reflector: a finished request wakes the loop when its TTL is up | `TestAFinishedRequestWakesTheLoopWhenItsTTLIsUp` |

## Considered and set aside

* **`dynamicinformer`.** It links the typed clientset and the
  informers for every built-in kind. The 2026-09-27 research measured
  a 39.1 MB binary with it, against 20.0 MB for the lean form.
* **A work queue for each object** (`util/workqueue`). The operator
  runs one full pass for each wake, so a handler that wakes the loop is
  enough.
* **One shared Go module with a loop of the project's own.** It keeps
  the guards exactly and costs no memory. The project would maintain
  the loop, its fake API server, and a release that every operator
  must take.

## The drill still owed

On liken-1, with the operator on a build of this change:

1. Read the operator container's working set on a 1 GB machine, and
   compare it with the build before this change.
2. Edit a `Peripheral`'s `spec.alias` and time how long bluetoothd's
   `Device1.Alias` takes to change. It must be the settle window,
   about 1.5 seconds.
3. Create a `PairingRequest` and check that its window opens in the
   same time.
