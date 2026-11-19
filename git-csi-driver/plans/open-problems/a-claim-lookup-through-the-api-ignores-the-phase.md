# A claim lookup through the API ignores the phase

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test gave the fake clientset of a node one `PersistentVolume` of the
handle, in the phase `Released`, with a `claimRef` to `home/config`,
and called `claimOf` for the handle. It wanted an error, because the
claim was deleted, and failed with:

```
claimOf returned the claim {namespace:home name:config} of a Released PersistentVolume and no error, want an error because that claim was deleted
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When the only `PersistentVolume` of a volume's handle is `Released`,
should the driver treat the volume as having no claim, and should
arming then keep retrying or leave the volume unarmed?

Whether the API path refuses a `PersistentVolume` that is not `Bound`
with the same error as one with no `claimRef`, and what each caller
does with that error. `arming.find` retries until its context ends.
