# Read from the cache

Read this file before you write or review a read of a watched kind, a
status write that can get a `409`, or the memo of the versions that
an operator wrote. `SKILL.md` has the rules of a watch and of a status
write.

Contents:

- The rule: which reads the store serves, and when
- The memo of the versions that the operator wrote or read
- A `409`
- When a direct GET stays
- The reference files

A pass reads the objects of a watched kind from the informer's store,
not from the API server. So a pass that changes nothing sends the API
server no read of a watched kind. The reads are in
`kubernetes/informer`. `kubernetes/memo` has the memo, which records
the version of each object that the operator last wrote or read. It
also has the requests whose responses the memo records (`ReadFresh`,
`ReadList`, `Written`, and `SettleStatus`). `kubernetes/memo` imports
nothing from `k8s.io`, so a program that must not link client-go can
link it.

## The rule

Read one object with `informer.ReadOne`, and a list with
`informer.List`. These read from the store only when the store is
ready (`informer.View.Ready`). A store is ready when it has the whole
first read, and the API server has accepted a watch and has not
forbidden one since. Before the store is ready, `ReadOne` and `List`
read from the API server. They also read from the API server after a
watch gets a `401` or a `403`, until the API server accepts a watch
again.

When `informer.List` reads from the API server, it sends the list
itself and records the version of each object that the list returns.
It does this because the store can become ready with a copy that is
older than the list: the watch's first read can come from the API
server's watch cache, which can be behind. A list that an operator
sends itself records nothing, so the next pass acts on the older copy.
The list sets no `resourceVersion`, so etcd serves it.

`library-operator` is the exception. It reads its member pods,
Players, MediaPreferences, Plays, people, claims, volumes, the pods
that it creates, progress pods, and nodes only from their stores, and
never from the API server. While the watch of one of these is
forbidden, the reflector lists the collection again after each
backoff, and the backoff grows to between thirty and sixty seconds.
The pass reads the result of that list from the store
(`library-operator/watch.go`, `items`).

A watch that fails for any other reason, such as a refused connection
while the API server restarts, leaves the store ready. So by default
the operator continues its local work from the store. `liken`'s
operators set `informer.Options.UnreadyOnWatchError` on every watch.
With that option, any failed watch makes the store not ready until the
API server accepts a watch again. A machine must not stage a rollout
or a reboot from a store that missed the writes made while its
reflector waited out a backoff.

When the store does not have an object, `ReadOne` reads it from the
API server. It also reads from the API server when the store's copy
does not convert, so a list does not leave out any object.

Use `informer.ReadSelected` to read one object when the watch's
selector covers every object that the read can ask for. That is true
for the objects that the operator creates and labels, and for the one
object that a watch follows by name. When such an object is not in a
ready store, and the memo has no record of it, `ReadSelected` returns
`apiclient.ErrNotFound` without a request. So a pass that creates a
missing object does not send a read to learn that it is missing. The
memo forgets the object after a `404`. It drops its record once the
store has the recorded version (`informer.ForgetStored`).
`astrophotography-operator` reads its workloads this way,
`library-operator` the objects that it creates, and `liken`'s
operators their `Machine` and `Cluster`.

## The memo

The store's copy of an object can be older than the operator's last
write to it. The watch delivers the write a moment after the write, and
later still while the watch is down. A pass that acts on the older
copy repeats a change that it already made. It opens a pairing window
again, sends a receiver a setting again, or skips a status write
because the older copy hides the need for it. So `memo.Versions`
records the `resourceVersion` of the newest copy of each object that
the operator wrote or read. When the store's copy is at another
version, the operator reads the object from the API server once, and
then reads from the store again. Compare versions only for equality.

Send every request whose response the memo records through
`memo.Versions.Send`. It runs one request for an object at a time. So
when two goroutines write the same object, the memo records their
responses in the order that the API server returned them.

- A failed request records that the operator has no current copy.
- A `404` records that the object is gone.
- Any other failure records the object as unknown. A `409` means that
  the object exists, and a write whose response was lost may have
  succeeded, so the memo cannot tell whether the object exists.

When a store has the whole collection (`informer.View.Whole`), a list
also includes each object that the operator created and that the store
does not have yet. The list reads each unknown object from the API
server, so a pass does not create it twice. `memo.Versions.ForgetGone`
keeps an unknown record until a read from the API server returns the
object or a `404`. Do not set `Whole` on a store with a selector,
because an object that the operator wrote can be outside the
selection.

## A 409

A write made from a copy that another writer has since changed gets
`409 Conflict`. `informer.SettleStatus` then reads the object from the
API server and composes the status again from the new copy. If the new
copy still needs the change, it writes once more. A `404` means that
the object is gone, and the caller treats it as absent. A server-side
apply sets no `resourceVersion`, so it never gets a `409`. An operator
that writes only by apply needs no retry, but it still records each
response.

## When a direct GET stays

Read from the API server in these cases, and say why in a comment at
the call:

- No watch has the kind, such as a `ResourceClaim` that a pass reads
  by name, or a `ResourceSlice`.
- The read must see another writer's write at once. For example, the
  owner `Adapter` of a new `Peripheral`: the store can have a UID from
  an `Adapter` that was deleted since.
- The read happens once at start, before the watch opens.

## The reference files

- `kubernetes/informer/cache.go` and `cache_test.go`: the reads, a
  list that runs while an object is created, and a list before the
  store is ready.
- `kubernetes/informer/selected.go` and `selected_test.go`: the read
  of a selected object by name.
- `kubernetes/memo/requests_test.go`: the status write after a `409`.
- `kubernetes/memo/memo_test.go`: the memo itself.
- `bluetooth-operator/objectcache_test.go`: the memo across three
  kinds, and a store that follows its selector.
- `display-operator/objectcache.go`: two goroutines that write one
  kind.
- `audio-operator/endpointwatch.go`: stores scoped by field selector.
- `equipment-operator/objectcache.go` with `cecbus_client.go`: writes
  by server-side apply through `memo.Written`, whole stores, and the
  creates that they must not repeat.

The test `TestAPassDoesNotActOnACopyOlderThanItsOwnWrite` fails
without the memo. It is in `bluetooth-operator/objectcache_test.go`,
`liken/machine-operator/pass_test.go`, and other operators. Not every
operator that uses the memo has this test, so search for the name
before you assume that a change to an operator's memo is tested.
