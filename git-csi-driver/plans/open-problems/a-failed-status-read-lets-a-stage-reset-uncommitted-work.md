# A failed status read lets a stage reset uncommitted work

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`reconcile` in `reconcile.go` brings a writeable volume's tree to
upstream at stage. Before it rewrites the tree, it checks for
uncommitted changes, because a reset or a rebase would overwrite them:

```go
if pending, _ := staging.work.pending(ctx); len(pending) > 0 {
```

The error from `pending` is dropped. When `git status` fails,
`pending` returns `nil`, the check passes, and a tree that is behind
upstream goes to `fastForward`. `fastForward` runs
`git reset --hard`, which deletes the pod's uncommitted changes. A
tree whose history split from upstream goes to `rebase`, where
`moveCheckout` runs `git read-tree -m -u`, which refuses to overwrite
a changed path. So the reset is the path that can lose work.

A diverged volume does not reach this check. `reconcile` calls
`stageDiverged` first, and `resetToUpstream` runs `git reset --hard`
when upstream contains the tree's HEAD, with no check for
uncommitted changes at all.

## What would confirm it

A test in `reconcile_test.go` with a tree behind upstream, an
uncommitted file, and a `git status` that fails, for example from a
damaged index. After the stage, the file is gone. A second test does
the same for a diverged volume whose side branch a person merged.

## What a fix has to decide

Whether a failed `pending` fails the stage, so the kubelet retries, or
leaves the tree as it is and records a fault, as `reportUpstreamMoved`
does for uncommitted changes. The diverged path needs the same
decision.
