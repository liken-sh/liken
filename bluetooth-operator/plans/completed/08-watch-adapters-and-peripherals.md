# 08, Watch the Adapters and the Peripherals

Built on 2026-09-27. No drill has run on liken-1 yet. The tests below
run the watch against a scripted API server. The drill that is still
owed is at the end of this plan.

This plan answers and replaces the open problem "An Adapter or
Peripheral edit waits for the backstop".

## The problem

The operator watched the `PairingRequest`s, so a new request or an
approval acted at once. It did not watch the `Adapter`s or the
`Peripheral`s. Three edits took effect only on the next pass: an edit
to an `Adapter`'s `spec.alias`, an edit to a `Peripheral`'s `spec`, and
the deletion of a `Peripheral`, which starts an unpair. With nothing
else to wake the loop, that pass was the backstop tick, at most 60
seconds later (`backstopInterval` in `main.go`).

The open problem kept the tick for one more reason: bluetoothd writes
some bond files with no D-Bus signal to announce them (`bondstore.go`).
That reason still holds, so this plan does not remove the tick. It
removes the edits from the list of changes the tick covers.

## The design

`editwatch.go` runs two watches through `listThenWatch` in `watch.go`,
the loop that already watches the `PairingRequest`s. There is one
watch loop in the repository, and it keeps the organization's three
guards: resume from the last version, list again only after a 410 and
only once, and decide the wait by how long the server kept the watch
open.

### What wakes the loop

An event wakes the loop when it carries an edit that the pass must act
on:

* a change to the spec. `metadata.generation` is the API server's
  count of spec changes, and a write to the status subresource does
  not change it.
* a deletion request, which the watcher reads from
  `deletionTimestamp`.
* a new object, and an object the API server removed. A `Peripheral`
  that somebody removed by force before its unpair finished leaves a
  bond in bluetoothd, and the next pass adopts that bond again.

Each list also wakes the loop once. A list follows a start or a gap in
the watch, and the watcher cannot tell which edits the gap held.

The operator writes status on these objects on many passes: a battery
level, a `Connected` condition, the node that holds the radio. The
watcher compares only the generation and the deletion mark, so the
event of the operator's own status write does not wake a second pass.

### What each watch selects

* The `Peripheral` watch selects the adapter's label,
  `bluetooth.liken.sh/adapter`, the same selector the pass lists with.
  Every pod writes the status of its own `Peripheral`s, and a watch of
  every `Peripheral` in the cluster would receive each other node's
  battery writes. The pass reports the address of the radio it reads
  through `inventory.follow`, and the watch opens with that address.
  When the address changes, the watcher closes the old watch before it
  opens the new one.
* The `Adapter` watch covers the whole cluster. The pass also releases
  a deleting `Adapter` whose radio left this node, and that `Adapter`
  carries another radio's label. A cluster has one `Adapter` for each
  radio, and its status changes only when its radio moves or its power
  changes. The watcher ignores an `Adapter` whose `status.node` names
  another node, unless it is the `Adapter` for the radio this pod
  holds.

The ClusterRole already granted `list` and `watch` on both kinds, so
the RBAC does not change.

### A collection with a query

`streamChanges` built the watch path as the collection, a `?`, and the
watch parameters. The `Peripheral` collection carries a label selector
of its own, so the watch parameters now join its query with `&`.

### The backstop

The tick stays at 60 seconds. The comment at `backstopInterval` names
the two failures it still covers: a bond file that bluetoothd wrote
with no D-Bus signal, and a pass whose read or write failed and whose
one retry failed too.

## Tests

The watch loop's tests (`watch_test.go`, `watch_faults_test.go`) now
cover every scenario in the organization's list:

| Scenario | Test |
|---|---|
| A watch runs and closes cleanly | `TestTheWatchStartsWhereItsLastSourceEnded` |
| A watch closes in under a second | `TestAFailedWatchWaitsBeforeTheNextRequest` |
| A `410` response, then a watch that works | `TestA410ResponseListsAgainAtOnce` |
| A `410` on every version | `TestAFailedWatchWaitsBeforeTheNextRequest` |
| A `410` after a watch that ran a second | `TestA410AfterAWatchThatRanListsAtOnce` |
| A non-410 `ERROR` event, stream held open | `TestAFailedWatchThatStaysOpenListsAgainAfterTheBackoff` |
| An object that does not decode, stream held open | `TestAFailedWatchThatStaysOpenListsAgainAfterTheBackoff` |
| A connection reset after a watch that ran | `TestADroppedConnectionResumesAtTheLastVersion` |
| A `403`, then the permission is granted | `TestARefusedWatchBacksOffUntilTheGrantArrives` |
| A dead server | `TestADeadServerGrowsTheBackoff`, `TestTheBackoffStopsAtItsCap` |
| A slow `200`, then a normal stream | `TestASlowAcceptDeliversEveryEvent` |
| A slow refusal or a slow `410` | `TestASlowRefusalStillGrowsTheBackoff`, `TestASlow410StillGrowsTheBackoff` |

The tests run each watch for about a second, not for minutes.

`editwatch_test.go` covers the wake rule: a status write does not wake
the loop, and a spec edit, a deletion request, a new object, and a
removed object do. An `Adapter` that another node holds does not wake
it. A `Peripheral` watch against the scripted server wakes on a spec
edit and not on the status write before it, and the watch moves to a
new radio's label when the pass reports a new address.

## Considered and set aside

* **client-go's informers.** They hold the same guards, but the
  organization has not chosen between them and one shared watch
  module, and the binary cost of client-go is not measured. This
  operator already has a loop that passes the scenarios.
* **Watch every `Peripheral` in the cluster.** It needs no address
  from the pass, but each pod would receive the status writes of every
  other node.
* **A field selector on `Adapter`'s `status.node`.** It would need
  `selectableFields` in the CRD, and a cluster has too few `Adapter`s
  for the saving to matter.
* **Remove the backstop.** The unannounced bond file still needs it.

## The drill still owed

On liken-1, with the operator on a build of this change:

1. Edit a `Peripheral`'s `spec.alias` and time how long bluetoothd's
   `Device1.Alias` takes to change. It must be the settle window,
   about 1.5 seconds, and not up to 60 seconds.
2. Delete a `Peripheral` and check that the unpair starts in the same
   time.
3. With no edits for an hour, compare the rate of
   `bluetooth_reconcile_duration_seconds_count{kind="Peripheral"}`
   with the rate before the change. The status writes must add no
   passes.
