# A claim lookup through the API ignores the phase

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`claimOf` in `arming.go` finds the claim of a volume handle in two
ways. It reads the store of the node's `PersistentVolume` watch first,
and takes the `claimRef` only from a copy whose phase is `Bound`.
When the store has no such copy, it lists `PersistentVolume`s from the
API server and returns the `claimRef` of the first one with the
handle, in any phase. A `Released` `PersistentVolume` keeps the
`claimRef` of the claim that was deleted.

So a handle whose store copy is `Released` falls through to the list,
and the list returns the same old claim that the store check refused.
The callers are `arming.find`, which arms the volume from the class of
that claim, `findClaim` in `readonly.go`, and `claimFor` in
`diverged.go`, which post Events on it and label metrics with it.

Plan 14 in `plans/completed/` gives the reason for the store check: a
copy in the store can lag the API server. It keeps the API list as it
was. The kubelet stages a volume only after its `PersistentVolume` is
bound, so the case needs a lookup after a release, which a retry of
`arming.find` or a later `claimFor` can be.

## What would confirm it

A case in `arming_test.go` with a fake clientset that holds one
`Released` `PersistentVolume` of the handle with a `claimRef`.
`claimOf` returns that claim and no error.

## What a fix has to decide

Whether the API path refuses a `PersistentVolume` that is not `Bound`
with the same error as one with no `claimRef`, and what each caller
does with that error. `arming.find` retries until its context ends.
