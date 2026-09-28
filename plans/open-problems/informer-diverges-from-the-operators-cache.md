# Keep the informer and the operators' cache in agreement

Open problem, low priority. `liken`'s
[kubernetes/informer](../../kubernetes/informer/) package and the
cache in the operator repositories solve the same problem with two
designs in two codebases. A defect found and fixed in one does not
reach the other, and each fix needs its own review of the other
design.

## The two designs

Both keep an in-memory copy of a watched collection through client-go's
reflector, and both have to handle one lag: a pass that runs right
after the operator's own write can read a copy that does not hold the
write yet.

* **`liken`.** [writes.go](../../kubernetes/informer/writes.go) records
  each write this process made (`Wrote`) until the watch delivers the
  version the write returned. While any record is open, `List` and
  `ByIndex` do not answer at all, and `Get` does not answer for that
  object. The caller then reads the API server.
* **The operators.** Six operator repositories (bluetooth, audio,
  display, equipment, media, and library) each keep their own copy of
  the cache in `objectcache.go`, and library-operator keeps its
  `versionMemo` in a separate `versionmemo.go`. `versionMemo` records the
  `resourceVersion` of each object's newest copy that the operator
  wrote or read. `currentList` answers from the store, and reads from
  the API server only the objects whose stored copy is at another
  version. `settleStatus` reads the object again after a `409` and
  writes once more. The `operators` skill in `.agents` describes this
  design, and does not name `liken`'s package.

## Two fixes to the operators' cache, checked against `liken`

Checked on 2026-09-28, against the code at that date:

* **The list race** (equipment-operator 94b3b35). `currentList` read the
  store's keys and then the memo's keys of objects the store did not
  hold. A create whose watch event arrived between the two reads was in
  neither, and the next pass created the object again. `liken` does not
  have this race. `List` checks for open records and then reads the
  store once. `Wrote` checks the store under the same lock that the
  watch handler takes to close a record, and the informer updates the
  store before it calls the handler. So an object that this process
  created is either in the one read of the store, or its open record
  makes `List` refuse to answer.
* **The re-read after a `409`.** The machine operator's
  `publishOwnStatus` in
  [reconcile.go](../../machine-operator/reconcile.go) reads its
  `Machine` from the API server after a conflict, not from the copy, and
  writes once more. The cluster operator does not retry a conflict on
  another machine's status on purpose: the conflict means that machine
  wrote its own status first. Neither case reads the stale copy again.

## Options

1. **Keep two designs, and link them.** Name `liken`'s package in the
   `operators` skill, and name the skill's cache design in the package
   comment of `kubernetes/informer`. A review of a fix to either one
   then checks the other. It needs no code change, and it does not stop
   the two designs from drifting apart.
2. **One package in one module.** Move one design into a Go module that
   `liken` and the six operators import, in place of the seven copies
   in the repositories now. `liken` imports only the dynamic client,
   `tools/cache`, and `rest`, so that the machine operator's binary
   stays small, and the shared package must keep that property.
3. **Move `liken` to the operators' design.** The per-object memo lets a
   pass answer a list while one of its own writes is open, where `liken`
   reads the whole list from the API server. That saves one list request
   after each write, and only while a write is open.

## What is not known

Whether the whole-list refusal in `liken` costs enough to matter. The
cluster operator writes grants and verdicts a few times for each
rollout, so a list read from the API server after each write is a few
requests. No measurement exists.
