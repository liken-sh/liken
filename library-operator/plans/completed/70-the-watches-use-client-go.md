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

## Reads after the operator's own write, 2026-09-27

The pass read every watched collection from its informer's store, and a
store's copy can be older than the operator's own last write. The watch
delivers a write a moment after the API server answers it, and later while
the watch is down. Two decisions read such a copy:

- `writeLibraryStatus`, `writeCatalogStatus`, and `checkProvider` compare
  the status they derive with the copy's status. An older copy can equal
  the derived status while the API server holds the write after it, so the
  pass skips the write and the wrong status stays until the next write.
- `standJellyfinBackfill` reads `status.jellyfin` on the `Catalog`. After the
  finished backfill `Job` is deleted, a copy from before the `Finished`
  write makes the pass create the `Job` again. Plan 50 says a second run is
  safe, so the cost is one more run against the Jellyfin server.

The Libraries, the Catalogs, and the MetadataProviders are now read the way
bluetooth-, display-, audio-, and equipment-operator read their stores.
`versionmemo.go` holds the memo, and every write of the three kinds notes
the `resourceVersion` the API server answered. `objectcache.go` reads an
object from the API server when the store's copy has another version.
`TestAPassDoesNotActOnACopyOlderThanItsOwnWrite` creates the backfill `Job`
a second time without the memo, and creates none with it. The Plays and the
people are written only by a patch that states the copy's
`resourceVersion`, so an older copy of one gets a `409` and the next pass
tries again. Their stores have no memo.

Two parts differ from the other operators. The operator creates none of the
three kinds, so a list reads no object that the store does not hold yet. A
`409` on a status write is not retried in the same pass. The memo sends the
next pass to the API server for the object, and the backstop tick runs that
pass within ten seconds. A fresh read of one `MetadataProvider` that
fails leaves the pass with the store's copies of every provider, because
the pass reads a failed read of the providers as no provider, and every
`Library` would then report its sources as missing. Before a store holds
its first read, the pass answers the informer's last error, as it does for
the other five kinds, so a cluster that serves no `MetadataProvider` costs
no request on each pass.

A settled pass reads no `Catalog` from the API server
(`TestASettledPassReadsNoCatalogFromTheAPIServer`). A pass that follows the
operator's own write sends one `GET` for each object it wrote, until the
watch delivers the write.

The same review found one more request in every settled pass. A `Catalog`
with no `spec.jellyfin` sent a `DELETE` for its backfill `Job` on each pass,
and the API server answered `404`. On `liken-1` that was six requests a
minute. The pass now sends the `DELETE` only when the `Jobs` it listed hold
the backfill `Job`. In the fake API server, the third pass over one
`Library` and one `Catalog` sent 22 requests before and 21 after. Eight of
those are the lists the test harness sends in place of the stores.

## Every read from a watch, 2026-09-28

On `liken-1`, a pass still sent a cluster-wide list of the claims, two of
the volumes, two of the pods, one of the Jobs, and one of the nodes. In a
sixty-second sample the operator ran 32 passes. Six came from the backstop
tick. The rest came from reports: every report that differed woke a pass,
and while an enrich Job filled gaps, its gap counts changed every few
seconds.

**The watches.** The operator now watches the claims, the volumes, and the
nodes whole, because a Library names any claim and a bound claim names any
volume. The node store drops `status.images`, which the operator never
reads. It watches its own pods, worker `Jobs`, progress member pods,
`Services`, `EndpointSlices`, people `ConfigMaps`, and trickplay
`ResourceClaimTemplates` by their labels. The gossip `Services` gain the
name label so the watch selects them. On the first pass after the upgrade
the store does not hold them, so the create meets a `409` and the memo
sends the next pass to the API server, which reads each one and writes the
label onto it. Two deletes now name the uid of the copy they read: the
sweep of released volumes, and the heal's forced delete of a stranded copy,
so a volume or a pod the pass wrote again under the same name is never
taken.

**The shared core.** `objectcache.go` and `versionmemo.go` are now the code
bluetooth-, display-, audio-, media-, and equipment-operator share,
including equipment's order of the two key reads in `currentList` and
media's `forgetGone`. Four parts differ:

- Every function that sends a request takes a context first, because this
  operator's `Client` takes one on every request and a pass bounds all of
  its requests with one deadline.
- The functions are split across the two files, because the pod build
  links no client-go, and the writes that note the memo are in both builds.
- `readStood` reads one object the operator stands by name. A store that
  holds its first read answers an object it does not hold, and the memo has
  not noted, as absent, with no request.
- `currentOrCached` keeps the store's copies of the MetadataProviders when a
  fresh read of one fails, because the pass reads a failed read of them as
  no provider at all.

The worker `Jobs` are read through the memo from a whole store, because a
library Job's name holds the time it was created: a second create of one
never meets a `409`, and a pass that read a store without the Job it just
created would create another walk. The memo forgets each Job that is gone.

A `409` on a status write now reads the object again, composes the status
from the fresh copy, and writes once more in the same pass (`settleStatus`),
as the other operators do. The per-kind status PUT helpers are gone;
`replaceStatus` writes all three kinds.

**Report wakes.** A report wakes a pass at once only when it changes a
decision: a run that starts or ends, a walk that starts or ends, a fact
whose gap opens or closes, or an oldest attempt that moves. A report that
changes only a count the status carries wakes nothing, and the next pass
writes it. A settle window was the other shape. It would still run a pass
for each burst of count changes, and every status field would still be
written by the one writer that derives the whole status.

**The tick.** The ten-second tick stays, as a clock for the decisions that
fall due with no event, and for the report counts above. A pass reads every
watched object from its store. The comment at `backstopInterval` names the
reads that are not from a store. The one on every pass is the `Secret` each
keyed `MetadataProvider` names, because the pass compares its
`resourceVersion` to decide whether a check is due, and no label selects a
`Secret` a person names.

**The numbers.** In the fake API server, through the real informers, a
settled pass over two namespaces, two `Libraries` (one with a render block),
two `Catalogs`, a delegated `Player`, and a `Person` sent 20 requests before
and none after (`TestASteadyPassListsNoWatchedCollectionAndWakesNothing`).
A pass a report wakes reads nothing either, and sends only the writes it
makes. With a keyed `MetadataProvider`, each pass also sends one `GET` of
its `Secret`.

## The `Secret` read only for a check, 2026-09-28

The `GET` of each keyed provider's `Secret` on every pass is gone. The
operator reads the `Secret` only when the provider's check call is due,
so a settled pass with a keyed `MetadataProvider` also sends no
request. A `Reachable` verdict is kept for an hour, and every other
verdict for five minutes, so a repaired key shows within five minutes.
An OMDb key that has spent its calls for the day reads as
`LimitReached` and is kept for the hour, because OMDb answers it with
the same `401` as a key it does not accept. A verdict whose status
write fails is written from the operator's note on the next pass, with
no second call. A `Secret` deleted after a `Reachable` verdict still
shows only at the next hourly call. (2026-10-10: that open problem is
closed. The pass now reads each `Ready` provider's `Secret` before it
creates a `Job`, and writes `NoSecret` when the `Secret` or its key is
gone.)

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
