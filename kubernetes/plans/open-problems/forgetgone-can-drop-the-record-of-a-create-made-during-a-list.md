# `ForgetGone` can drop the record of a create made during a list

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

An operator calls `memo.Versions.ForgetGone` after each
`informer.CurrentList`, with the keys that the list returned.
`ForgetGone` drops the record of each key that is in neither that
list nor the store. The doc of `ForgetGone` says that the list must
include the keys that `Unheld` returned, so that the record of an
object the operator created, and that the watch has not delivered
yet, stays. That holds only for a create whose `Send` finished before
`CurrentList` called `Unheld`.

When another goroutine creates an object through `Send` after
`CurrentList` called `Unheld`, and the watch has not delivered the
object when `ForgetGone` runs, the key is in neither the list nor the
store. `ForgetGone` drops its record. The next pass then gets no key
from `Unheld` and no copy from the store, so it does not see the
object and can create it again. A create by name then fails with a
409, and a create with `generateName` makes a second object.

## Evidence

- `CurrentList` in `kubernetes/informer/cache.go` reads
  `Versions.Unheld` once, at the start of the list.
- `ForgetGone` in `kubernetes/memo/memo.go` skips only a listed key,
  an unknown record, and a key that the store has. It does not
  compare `recordedAt` with a count taken when the list started.
  `SendList` in the same file makes that comparison to keep a record
  that a `Send` wrote while its list was in flight.
- The callers are `currentKind` in `media-operator/objectcache.go`,
  `currentJobs` in `library-operator/watch.go`, `promotion.go` in
  `astrophotography-operator`, and `watches.go` in
  `liken/cluster-operator`.

## The reproduction

A test in `kubernetes/memo` calls `Unheld` on an empty store, then
`Send` for a new key that returns a version, as a create on another
goroutine does. Then it calls `ForgetGone` with the keys that `Unheld`
returned and the same empty store, and asserts that `Noted` is still
true for the new key. It failed with:

```
Noted(studio) = false after ForgetGone, want true because the create came after the list read Unheld
```

The test is not in the tree, because a failing test cannot land on
`main`. It does not show whether any caller sends a create for the
kind while a pass runs `CurrentList`. That is still to check for each
caller.

## What a fix has to decide

When an operator creates an object while a pass lists that kind, the
pass can forget the create and make the object again. The choice is
between these:

- Whether `ForgetGone` keeps each record written after a point that
  the caller takes before the list, as `SendList` does with
  `recordCount`, and how the caller passes that point.
- Or whether the callers must never send requests about the kind
  while a list runs, and the doc of `ForgetGone` states that rule.
