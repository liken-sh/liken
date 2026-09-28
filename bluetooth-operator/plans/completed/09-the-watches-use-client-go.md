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
