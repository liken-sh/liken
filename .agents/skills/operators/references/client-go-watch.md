# Watch a collection with client-go

Read this file before you write or review the code of a watch: an
informer, its handlers, its imports, or the test of its handlers.
`SKILL.md` has the decision to watch through client-go and the guards
that every watch must meet.

Contents:

- The lean form: the imports and the decode
- The measured cost of the lean form
- The differences between the reflector and the guards
- The code of a watch
- The test of the handlers

## The lean form

An operator imports only `k8s.io/client-go/tools/cache`,
`k8s.io/client-go/dynamic`, and `k8s.io/client-go/rest`. It decodes
each object into its own struct with
`runtime.DefaultUnstructuredConverter`. It does not import
`dynamicinformer`, `informers`, or `kubernetes` (the typed clientset),
because those link a client and an informer for every built-in kind,
and that doubles the size of the binary. The operator keeps its own
client for reads and writes. Only the watches use client-go.

`k8s.io/client-go/util/workqueue` is allowed only when a queue for
each object makes the code simpler.

The client-go version follows the Kubernetes minor version that
`liken/k3s/VERSION` pins. `ci pins` fails when two modules use
different versions of one dependency.

## The measured cost

`bluetooth-operator` plan 09 measured its port on 2026-09-27. Each
binary ran only its three watches against a k3s v1.36.3 API server in
Docker, with client-go v0.36.3, and linked every package of the
operator.

| | Hand-written loop | client-go, lean form |
|---|---|---|
| Stripped binary (the image is `FROM scratch`, so the same) | 15.0 MB | 19.9 MB |
| Linked Go packages | 354 | 474 |
| RSS, idle, 50 objects | 16.0 to 16.9 MB | 21.0 to 22.4 MB |
| RSS, idle, 250 objects | 18.4 MB | 25.5 MB |

The earlier research used one watch, and it measured a binary of
39.1 MB that used `dynamicinformer`. A transform that removes
`metadata.managedFields` before the informer stores an object lowered
the RSS at 250 objects from 28.2 MB to 25.5 MB. On that date, a
two-node test cluster ran seven operator pods on one node and four on
the other that did not link client-go yet. At 5 to 7 MB for each pod,
the port cost about 20 to 50 MB on each node, which matters on a
1 GB machine. The port removed 309 lines of code and 682 lines of
tests, and added 264 lines of code and 326 lines of tests.

## The differences from the guards

The reflector does not meet the guards in `SKILL.md` exactly. These
differences come from a reading of `tools/cache/reflector.go` in
client-go v0.36.3:

1. After a `410`, it waits a backoff of 0.8 to 1.6 seconds before it
   reads the collection again.
2. When a watch closes in under a second with no event, the reflector
   reads the collection again after the backoff. It does not resume.
3. It never treats a watch that delivered an event as short.
4. It measures a watch's life from the request, not from the `200`.
5. Its backoff resets after two minutes with no failure, not after one
   watch that ran for a second.

The organization accepts them, because none of them loses an event.
Differences 1 and 2 cost one more read of the collection, or a read
that happens a second later. Differences 3, 4, and 5 change only how
long the reflector waits during a fault. The reflector also reads with
a streaming list (`WatchListClient`, on by default from v0.36). When
the API server rejects a streaming list, the reflector uses a plain
list.

## The code of a watch

`kubernetes/informer` has the skeleton below. `Start` runs one watch
of a `Source`, with the `Handler`, `Synced`, and `Reopened` of its
`Options`. `Convert` decodes each object, and `Report` logs an object
that does not decode. `bluetooth-operator` is the reference for the
handlers: they are in `requestwatch.go` and `editwatch.go`, and the
tests beside each file run them through the real reflector against
the fake server of `watch_test.go`.
`liken/kubernetes/watch` has `liken`'s handlers. `WakeOnChange`,
`WakeOnEdit`, and `WakeOnContent` are for a collection whose
`Transform` trims each object to the fields that the pass reads.
`WakeOnAnotherWritersChange` is for an object whose status the pass
writes and another writer also changes.

- **One informer for each collection.** Build a `cache.ListWatch`
  whose `ListWithContextFunc` and `WatchFuncWithContext` call the
  dynamic client, and set the label selector in both. Pass it to
  `cache.NewInformerWithOptions` with `&unstructured.Unstructured{}` as
  the object type. Run it with `RunWithContext`, which returns after
  the last handler call. Pass a `Transform` that removes
  `metadata.managedFields`.
- **Convert, and report what does not convert.** A handler receives an
  `*unstructured.Unstructured`. For an object that was deleted while
  the watch was down, it receives a `cache.DeletedFinalStateUnknown`
  tombstone instead. Unwrap the tombstone, and decode with
  `runtime.DefaultUnstructuredConverter`. Log an object that does not
  convert, with its kind and name, and do not drop it silently. A
  tombstone can have no copy of the object at all. In that case,
  forget the object by the tombstone's key, or wake the pass.
- **A handler wakes the pass.** An operator that runs one full pass
  for each wake needs no queue. The handler sends on a channel with
  one slot, in a `select` that has a `default`, so a burst of events
  makes one wake. Sometimes the pass can read a collection before the
  informer's first read does. Then the watch also wakes the pass once
  when the informer has synced, so that an edit between the two reads
  is not lost. Wait on `HasSyncedChecker().Done()`, not on
  `WaitForCacheSync`, which polls. Join that goroutine before the
  channel closes.
- **Filter the operator's own status writes.** `UpdateFunc` receives
  the informer's previous copy and the new copy. Wake only when
  `metadata.generation`, `metadata.deletionTimestamp`, or
  `metadata.uid` differ between them. A write to the status
  subresource changes none of them. After a gap in the watch, the
  informer reports each difference from its previous copy as an add,
  an update, or a delete, so the same rule covers the gap. The UID is
  in the comparison because an object that was deleted and created
  again with the same name during the gap arrives as an update.
- **Restart a watch whose selector changes.** A selector is fixed for
  the life of an informer. When the source of the selector changes,
  such as the address of the radio that the pass reads, cancel the
  informer's context. Wait until `RunWithContext` has returned, and
  start a new informer with the new selector (`followPeripherals` in
  `editwatch.go`).
- **Follow one object by name with `informer.WatchOne`.** It selects
  the object with the field selector `metadata.name`, because that is
  how RBAC matches a list and a watch against a Role's
  `resourceNames`. It gives the owner each version of the object. It
  gives nil when the object does not exist, and that includes the end
  of a first read that finds nothing. For example, the API pod of
  `audio-operator` follows its capture `Secret` and the
  `ConfigMap` of the client CA this way (`followCertificateObjects` in
  `audio-operator/api.go`).

## The test of the handlers

Point the dynamic client at a server from `kubernetes/apiservertest`
that serves a streaming list. The server sends an `ADDED` event for
each object, then a `BOOKMARK` with the annotation
`k8s.io/initial-events-end: "true"`, and then the script's events on
the same stream. Test the wake rule through that server, and leave the
reflector's faults to upstream. The server uses in-memory
connections, so the test runs in a `synctest` bubble and waits out a
backoff on the fake clock.
