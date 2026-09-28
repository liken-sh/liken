---
name: operators
description: How the liken-sh operators keep their view of the cluster and their devices current. Covers the three guards every Kubernetes watch loop needs, the scenarios a watch loop must pass in a test, status writes, timers, device traffic, and the decision to watch through client-go with the shape of the reference port. Use when writing or reviewing an operator's watch, reconcile pass, backstop, timer, or device polling.
---

# Writing and reviewing operators

The organization's rule is in `AGENTS.md`, "Keep state current with
events": open the watch or subscription first, read the whole state
once, and open it again and read again when it fails. A timer is
correct only as a clock. This skill carries the detail that the rule
needs in an operator.

In 2026-09, reviews of seven hand-written watch loops found the same
faults again and again, and each loop needed several rounds to meet
the guards below. The organization then chose client-go's reflector
for every operator (see the decision at the end). Read this skill
before you write, change, or review a watch, and check it against
every guard and scenario here.

## The three guards of a watch loop

1. **Resume, do not list.** List first, then watch from the list's
   `resourceVersion`, with `allowWatchBookmarks=true` so that the
   version moves while nothing changes. When a watch closes, open the
   next one from the last `resourceVersion` it delivered. Do not list
   again. A watch with no `resourceVersion` replays every object first,
   so each such open costs as much as a list.
2. **List again only for a 410, and only once.** A `410 Gone`, as the
   HTTP response or as an `ERROR` event, lists again at once. If the
   watch that opens from that fresh list also gets a 410, wait out the
   backoff. A 410 that ends a watch which ran a second or longer
   counts as a first 410. After any other `ERROR` event, and after an
   event whose object or line does not decode, wait out the backoff,
   then list. On any error, close the stream at once: the server holds
   a watch open for minutes, and a loop that reads the stream to its
   end loses every event in that time.
3. **Decide the wait by the watch's life.** A watch that closes less
   than a second after the server accepted it is a failure, whatever
   it delivered, so the backoff applies and grows to its cap. A watch
   that ran a second or longer resets the backoff, even when it ended
   with an error. Measure the life from the moment the `200` arrived,
   not from the request: a slow dial, a slow refusal, and a slow `410`
   answer never ran.

## Scenarios every watch loop must pass

Test a loop against a scripted fake API server that can hold a stream
open, answer slowly, and cut a connection. Each scenario names the
correct result.

| Scenario | Correct result |
|---|---|
| A watch runs for minutes and closes cleanly | Reopen at once from the last version, with no list |
| A watch closes in under a second | Wait out the backoff, then reopen from the last version |
| A `410` response, then a watch that works | One list at once |
| A `410` on every version | Lists bounded by the backoff |
| A `410` after a watch that ran a second | One list at once |
| A non-410 `ERROR` event, stream held open | Close at once, wait, list |
| An object or a line that does not decode, stream held open | Close at once, wait, list |
| A connection reset after a watch that ran | Resume from the last version, with no list |
| A `403`, then the permission is granted | Back off while refused, then watch normally |
| A dead server (the dial fails) | The backoff grows to its cap |
| A slow `200`, then a normal stream for minutes | Every event delivered |
| A slow refusal or a slow `410` | The backoff still grows |

## Status, echoes, and the pass

- **Write status only on a change.** A status field that changes on
  every pass, such as a timestamp or a field the CRD schema drops,
  makes every pass write, and the write wakes every watcher. Compare
  against what the API server stores, not against what the code
  meant to write.
- **Scope each watch to what the process owns.** A DaemonSet pod that
  watches every object of a kind wakes on every other node's writes.
  A CRD can declare `selectableFields`, so each pod lists and watches
  by field selector, for example `status.node=<machine>`.
- **A field-selected list needs the `list` verb.** RBAC authorizes a
  list or a watch against `resourceNames` only when the request
  selects that one name, and the Role must grant `list` as well as
  `watch`.
- **A CRD change reinitializes its storage.** During a rollout that
  changes a CRD, the API server answers `429 storage is
  (re)initializing` for a short time. A loop must back off and retry.
- **Read once per pass, not once per object.** A pass that GETs each
  claim or pod it stands costs one request per object on every pass.
  One list per pass answers the same questions.

## Timers and devices

- **Name what a kept timer covers.** A backstop tick needs a comment
  at the timer that names each failure it covers and why no event
  covers it. A timer that is a clock (a deadline, a TTL, a backoff, a
  heartbeat) needs only a correct comment.
- **Traffic to a device is a cost even when it is cheap.** A TV
  switched its input while an operator scanned its CEC bus each
  minute. Ask a device only when a person's action or an announcement
  needs the answer, bound each question to one per event, and share
  one read in flight between every caller that needs the same fact.
- **A lost event wakes a full read.** A uevent socket that answers
  `ENOBUFS` dropped events. Wake the pass at once instead of waiting
  for the next event or the backstop.
- **Signal a child through its process handle.** A signal to a bare
  pid can reach a later process that reused it. `os.Process` holds a
  pidfd, and a signal after the process was reaped returns
  `ErrProcessDone`.

## Decision: every watch runs on client-go

Decided on 2026-09-27: every operator in the organization watches the
API server through client-go's reflector, in the lean form below, and
deletes its hand-written loop. The reason is maintenance. Eight
repositories each held one loop, about 2,470 lines in all (1,530 of
code) and 4,900 lines of tests for the loops alone. The loops drifted
apart, and each fix landed in one repository at a time. Upstream
maintains and tests the reflector, so the project stops owning eight
loops. The choice set aside was one shared Go module with a loop of the
project's own. It kept the guards exactly and cost no memory, and the
project would have maintained the loop, its fake API server, and a
release that every operator must take.

**The lean form.** An operator imports only
`k8s.io/client-go/tools/cache`, `k8s.io/client-go/dynamic`, and
`k8s.io/client-go/rest`, and decodes each object into its own struct
with `runtime.DefaultUnstructuredConverter`. It never imports
`dynamicinformer`, `informers`, or `kubernetes` (the typed clientset).
Those link a client and an informer for every built-in kind, which
doubles the binary. The operator's own client for reads and writes
stays; only the watching moves.

**The measured cost.** `bluetooth-operator` plan 09 measured its port
on 2026-09-27. Each binary ran only its three watches against a k3s
v1.36.3 API server in Docker, with client-go v0.36.3, and linked every
package of the operator.

| | Hand-written loop | client-go, lean form |
|---|---|---|
| Stripped binary (the image is `FROM scratch`, so the same) | 15.0 MB | 19.9 MB |
| Linked Go packages | 354 | 474 |
| RSS, idle, 50 objects | 16.0 to 16.9 MB | 21.0 to 22.4 MB |
| RSS, idle, 250 objects | 18.4 MB | 25.5 MB |

The earlier research, with one watch, measured 39.1 MB for a binary
that used `dynamicinformer`. A transform that removes
`metadata.managedFields` before the informer stores an object took the
RSS at 250 objects from 28.2 MB to 25.5 MB. A two-node test cluster
ran seven and four operator pods on its nodes that do not link
client-go yet. At 5 to 7 MB for each pod, the cost is about 20 to 50 MB
on each node, which matters on a 1 GB machine. The port removed 309
lines of code and 682 lines of tests, and added 264 lines of code and
326 lines of tests.

**The differences from the guards.** The reflector does not meet the
three guards above exactly. A reading of client-go v0.36.3's
`tools/cache/reflector.go` gives five differences:

1. After a `410`, it waits a backoff of 0.8 to 1.6 seconds before it
   reads the collection again.
2. A watch that closes in under a second with no event makes it read
   the collection again after the backoff. It does not resume.
3. A watch that delivered an event is never short.
4. It measures a watch's life from the request, not from the `200`.
5. Its backoff resets after two minutes with no failure, not after one
   watch that ran a second.

The organization accepts them because none of them loses an event.
Differences 1 and 2 cost one more read of the collection, or a read a
second later, and 3, 4, and 5 change only how long the reflector waits
during a fault. The reflector also reads with a streaming list
(`WatchListClient`, on by default in v0.36), and falls back to a plain
list when the API server refuses it.

The guards and the scenarios above still describe what any watch must
do, and a reviewer checks an operator's use of the reflector against
them. A handler that drops an object, a selector that does not follow
its source, and a loop of the operator's own around the informer each
break a guard.

**The migration order.** `bluetooth-operator` is done, and it is the
reference port (plan 09). The other operators follow one repository at
a time. `per-node-csi-driver` already runs a client-go informer, and
`git-csi-driver` already links client-go's typed clientset under its
hand-written loop. Each port measures its binary, its image, and the
idle RSS of its watches before and after, and records them in a plan. Until a
repository moves, its hand-written loop must still pass every scenario
above.

## Watch a collection with client-go

The reference port is `bluetooth-operator`: `watch.go` runs one watch,
`requestwatch.go` and `editwatch.go` hold the handlers, and
`watch_test.go` runs them through the real reflector.

- **Imports.** Allowed: `k8s.io/client-go/tools/cache`,
  `k8s.io/client-go/dynamic`, `k8s.io/client-go/rest`, and
  `k8s.io/client-go/util/workqueue` only when a queue for each object
  makes the code simpler. Forbidden: `dynamicinformer`, `informers`,
  and `kubernetes`. The client-go version follows the Kubernetes minor
  in `liken`'s `k3s/VERSION`: v1.36.x takes v0.36.x.
- **One informer for each collection.** Build a `cache.ListWatch`
  whose `ListWithContextFunc` and `WatchFuncWithContext` call the
  dynamic client, set the label selector in both, and pass it to
  `cache.NewInformerWithOptions` with `&unstructured.Unstructured{}` as
  the object type. Run it with `RunWithContext`, which returns after
  the last handler call. Pass a `Transform` that removes
  `metadata.managedFields`.
- **Convert, and report what does not convert.** A handler receives an
  `*unstructured.Unstructured`, or a `cache.DeletedFinalStateUnknown`
  tombstone for an object deleted while the watch was down. Unwrap the
  tombstone, decode with `runtime.DefaultUnstructuredConverter`, and
  log an object that does not convert, with its kind and name. Never
  drop one silently. A tombstone can hold no copy at all. Then forget
  the object by the tombstone's key, or wake the pass.
- **A handler wakes the pass.** An operator that runs one full pass for
  each wake needs no queue. The handler sends on a channel with one
  slot, with a `select` that has a `default`, so a burst of events
  makes one wake. When the pass can read a collection before the
  informer's first read does, the watch also wakes the pass once when
  the informer has synced, so an edit between the two reads is not
  lost. Wait on `HasSyncedChecker().Done()`, not `WaitForCacheSync`,
  which polls. Join that goroutine before the channel closes.
- **Filter the operator's own status writes.** `UpdateFunc` receives
  the copy the informer held and the new copy. Wake only when
  `metadata.generation`, `metadata.deletionTimestamp`, or
  `metadata.uid` differ between them. A write to the status
  subresource changes none of them. After a gap in the watch, the
  informer reports each difference from what it held as an add, an
  update, or a delete, so the same rule covers the gap. The UID is in
  the compare because an object deleted and created again with the
  same name during the gap arrives as an update.
- **Restart a watch whose selector changes.** A selector is fixed for
  the life of an informer. When its source changes, such as the
  address of the radio the pass reads, cancel the informer's context,
  wait until `RunWithContext` has returned, and start a new informer
  with the new selector (`followPeripherals` in `editwatch.go`).
- **Test the handlers, not the reflector.** Point the dynamic client
  at an `httptest` server that answers a streaming list: an `ADDED`
  event for each object, then a `BOOKMARK` whose annotations hold
  `k8s.io/initial-events-end: "true"`, then the script's events on the
  same stream. Test the wake rule through that server, and leave the
  reflector's faults to upstream.

## Read from the cache

A pass reads the objects a watch holds from the informer's store, not
from the API server. A settled pass then sends the API server no read
of a watched kind. Every operator reads the same way, with the same
types and functions in its `objectcache.go`, so a reader who knows one
knows all four.

- **The rule.** Read one object with `readOne` and a list with
  `currentList`. A list comes from a store only when the store holds
  the whole first read (`storeView.ready`). Before that, and when no
  watch runs, list from the API server. `readOne` reads an object the
  store does not hold from the API server. A copy that does not convert is
  read from the API server too, so a list leaves out no object.
- **The memo.** A store's copy can be older than the operator's own
  last write, because the watch delivers the write a moment later, and
  later still while the watch is down. A pass that acts on that copy
  acts again on a change it already made: it opens a pairing window
  again, sends a receiver a setting again, or skips a status write
  that the older copy hides. So `versionMemo` records the
  `resourceVersion` of each object's newest copy that the operator
  wrote or read. A store's copy at another version is read from the
  API server once, and then the store answers again. Compare versions
  only for equality. Send every request whose answer the memo notes
  through `versionMemo.send`, which holds one request for an object at
  a time, so two goroutines that write one object note their answers
  in the order the API server gave them. A failed request notes that
  the operator holds no current copy, because a write whose answer was
  lost may have landed. A store that holds the whole
  collection (`storeView.whole`) also lists each object the operator
  created that the store does not hold yet, so a pass does not create
  it twice. Do not set `whole` on a store with a selector: an object
  the operator wrote can be outside the selection.
- **A 409.** A write from a copy that another writer changed gets
  `409 Conflict`. `settleStatus` then reads the object from the API
  server, composes the status again from the fresh copy, and writes
  once more if the fresh copy still needs it. A `404` means the object
  is gone, and the caller handles it as absent. A server-side apply
  states no `resourceVersion` and gets no `409`, so an operator that
  writes only by apply needs no retry, and still notes each answer.
- **When a direct GET stays.** Read from the API server, and say why
  at the call, when no watch holds the kind (a `ResourceClaim` a pass
  reads by name, a `ResourceSlice`), when the read must see another
  writer's write at once (the owner `Adapter` of a new `Peripheral`,
  whose UID the store can hold from an object deleted since), and for
  a read once at start, before the watch opens.
- **The reference files.** `bluetooth-operator/objectcache.go` and
  `objectcache_test.go` (the memo across three kinds and a store that
  follows its selector), `display-operator/objectcache.go` (two
  goroutines that write one kind), `audio-operator/objectcache.go`
  (stores scoped by field selector), and
  `equipment-operator/objectcache.go` (writes by server-side apply,
  whole stores, and the creates they must not repeat). The test
  `TestAPassDoesNotActOnACopyOlderThanItsOwnWrite` in each repository
  fails without the memo, and `versionmemo_test.go` tests the memo
  itself.
