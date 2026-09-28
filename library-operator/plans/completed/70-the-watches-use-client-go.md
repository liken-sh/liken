# 70, The watches use client-go

Built on 2026-09-27. The operator watches its eight collections through
client-go's reflector, and a pass reads those eight collections from
the informers' copies instead of listing them from the API server. A
steady pass against the test cluster sends 22 requests, where it sent
30. The operator's own status writes and finalizer patches on a
`Library`, a `Catalog`, and a `MetadataProvider` no longer wake a pass.
The numbers below come from the fake API server in the tests, from a
k3s v1.36.3 API server in Docker, and from reads of `liken-1`. The drill
on `liken-1` is owed, at the end of this plan.

## The problem

The operator watched eight collections through one watch loop written
by hand, `watchloop.go` and `watch.go`, with 221 lines of tests for the
loop alone. Seven other repositories in the organization each had a
loop of the same kind, and reviews in 2026-09 found the same faults in
several of them. On 2026-09-27 the organization chose client-go's
reflector for every operator, in the lean form that the `operators`
skill in `.agents` records, because upstream maintains and tests the
reflector. `bluetooth-operator` plan 09 is the reference port.

The same audit counted about 13 cluster-wide lists in each pass of this
operator. Eight of them read collections that the operator already
watched: the `Library`, `Catalog`, `MetadataProvider`, `Player`,
`MediaPreferences`, `Play`, and `Person` objects, and the catalog member
pods. The watch carried each change, and the pass then read the whole
collection again from the API server.

## The design

### The watches

`watch.go` runs one informer (`cache.NewInformerWithOptions`) for each
collection, with the dynamic client. The operator imports only
`k8s.io/client-go/tools/cache`, `k8s.io/client-go/dynamic`, and
`k8s.io/client-go/rest` for this. The pod watch keeps its label
selector, `library.liken.sh/catalog=member`, in the list and in the
watch. A transform removes `metadata.managedFields` before the informer
stores an object.

`convert` decodes each object into the operator's own struct with
`runtime.DefaultUnstructuredConverter`, and unwraps the tombstone that
the informer sends for an object deleted while the watch was down. A
handler logs an object that does not convert, with its kind and name,
and wakes the pass.

A new object and a removed object wake the pass. An update wakes it by
one of two rules:

* **The objects a person declares and this operator writes the status
  of.** A `Library`, a `Catalog`, and a `MetadataProvider` wake the pass
  when the update changes `metadata.generation`, the deletion mark, or
  the UID. A write to the status subresource and a finalizer patch
  change none of them, so this operator's own writes wake no pass. The
  UID is in the compare because an object deleted and created again
  with the same name during a gap in the watch reaches the handler as
  an update.
* **The objects other writers change.** A `Player`, `MediaPreferences`,
  a `Play`, a `Person`, and a catalog member pod wake the pass when the
  operator's struct differs between the two copies, with the
  `resourceVersion` left out of the compare. The pass reads the status
  of these objects, which `media-operator`, `people-operator`, and the
  kubelet write. A write that changes only a field the operator's struct
  does not hold wakes nothing.

[Skipping the operator's own watch
echoes](../rejected/skipping-the-operators-own-watch-echoes.md) set a
filter of the same aim aside. That filter recorded the
`resourceVersion` of every write in a map that the client and every
watch shared, and dropped a version after a minute. The first rule
here holds no state: the informer hands `UpdateFunc` the copy it held
and the new copy, and the rule compares them. A `Library`'s status has
one writer, this operator, so the rule loses no wake that the pass
needs.

`library_watch_restarts_total` counts every watch the reflector opens
after the first one of a collection.

### The pass reads the informers

The pass reads the eight collections through the `collections`
interface in `operate.go`. `watch.go` answers each read from the
informer's store, in namespace and name order, the order of a list
from the API server. The informer writes a change into its store
before it calls the handler, so the pass that a change wakes reads that
change. The pass copies each object into a new struct, so a write in
the pass never changes the informer's copy.

A read keeps the terms of the list it replaced. An object that does not
convert fails the read of its whole collection, as one object that did
not decode failed a list, so a pass never reads a `Library` that is
there as one that is gone. A failed read of the `Library`, `Catalog`,
or member pod collection ends the pass, and a failed read of the other
five reads as an empty collection. The one exception is the Plays: a
pass that cannot read them skips the Plays, because read as empty they
would clear the retained marks of every `Play` that still exists, and a
deleting `Play` waits on its recorded mark to lose its finalizer. The
list this plan replaced had the same fault.

The first pass starts only when every collection has been read once,
or has failed its first read, within the pass timeout of 30 seconds. A
first pass on an informer that is still empty would read every
`Library` as gone and clear its topics. A failed first read of the
`Library`, `Catalog`, or member pod collection ends the operator, as a
failed first list did. The other five collections belong to operators
that a cluster may not run, and a collection that nobody serves reads
as empty until its informer reads it, and so does one that does not
answer within the bound.

The pass still lists what the operator does not watch: the `Jobs`, the
claims, the volumes, the pods it stands, the progress store pods, the
nodes, and the objects it reads by name. The operator does not watch a
kind only to cache it.

A write that conflicts is not retried in the same pass. The informer's
copy can be one write behind the API server for a few milliseconds, so
a pass that runs right after the pass that wrote a status can send a
write that conflicts. The conflict loses nothing: the next pass reads
the delivered copy, and the backstop tick bounds that wait at 10
seconds.

### The loop

The pass model does not change: one full pass for each wake, a wake
channel with one slot that merges a burst into one pass, and the
10-second backstop. A per-object work queue does not make this code
simpler. The pass derives every status from whole collections, and the
report desk, the bus, and the webhook server wake the same loop. The
backstop comment in `operate.go` names what the tick still covers: the
objects the pass reads with no watch, and the decisions that fall due
with time.

## What was removed

`watchloop.go` and its tests, the recovery the eight watches shared,
are gone, with the eight list functions the pass no longer sends,
`Client.Do`, and the tests of the list functions. The tests of a pass
read the collections from a lister in `listreads_test.go`, because a
test changes the fake cluster by hand between passes and no watch would
carry that change. `watch_test.go` runs the eight informers through
client-go's reflector against the fake cluster, whose watch streams
(`watchstreams_test.go`) answer a streaming list and send each change a
pass or a test makes.

## The numbers

**API requests.** A pass over two namespaces, two `Libraries`, two
`Catalogs` with their catalog pods, one delegated `Player`, and one
`Person` in the fake API server, after three passes had stood what the
cluster lacked:

| | Before | After |
|---|---|---|
| Requests in one steady pass | 30 | 22 |
| Lists of the eight watched collections | 8 | 0 |

`TestASteadyPassListsNoWatchedCollectionAndWakesNothing` holds the
second row for seven collections, and checks that the steady pass
wakes no pass after it. The fake records a request's path and not its
selector, so the test cannot tell the member pod list from the two pod
lists the pass still sends. The list function of the member pods is
deleted, so no code sends it.

**Passes at rest.** On `liken-1`, with three `Libraries` and no `Play`,
`library_reconcile_duration_seconds_count` rose by 102 in five minutes
before the port: 6.8 passes a minute, the 6 of the backstop and a few
wakes from a scheduled walk. A read-only count of watch events on the
same cluster in five minutes found one `Catalog` event and two pod
events. The ~27 passes a minute that the audit saw at rest did not
occur in this window. In the k3s API server in Docker, with two
`Libraries`, each build ran 6 passes a minute at rest, which is the
backstop alone. In its first minute, the build before the port ran 9
passes, and the build after it ran 5, because the echoes of its status
writes woke nothing.

**Binary and memory.** Each binary is built with `-trimpath -ldflags='-s
-w'`, and the RSS is the operator's steady `VmRSS` against the k3s API
server with two `Libraries` and one `Catalog`. The full build after the
port also carries plan 71's leader election, so its numbers are the
sum of both plans.

| | Before | After |
|---|---|---|
| The operator's stripped binary | 12.9 MB | 30.8 MB |
| The operator's linked packages | 265 | 728 |
| The operator's steady RSS | 19.3 MB | 31.3 MB |
| The pod build's stripped binary | 12.9 MB (one build) | 11.9 MB |
| The pod build's linked packages | 265 (one build) | 265 |

Plan 71 says why the pods and `Jobs` run a separate build.

## The drill that is owed

On `liken-1`, after the main session rolls the build:

1. Count `library_reconcile_duration_seconds_count` for five minutes at
   rest, and compare it with the 6.8 passes a minute above.
2. Edit a `Library`'s `spec.scan.schedule`, and check that one pass
   follows at once.
3. Let a scan run, and check that its status writes wake no pass of
   their own.
4. Read `library_watch_restarts_total` after an hour, and check the
   operator's log for a line that names an object that does not
   convert.
