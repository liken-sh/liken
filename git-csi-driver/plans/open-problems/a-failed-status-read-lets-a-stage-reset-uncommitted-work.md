# A failed status read lets a stage reset uncommitted work

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and unit tests reproduced it on 2026-11-19.

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
uncommitted changes at all. So a volume whose side branch a person
merged loses its uncommitted changes at the next stage, even when
`git status` works.

## The reproduction

One test staged a writeable volume, changed
the tracked file `a.txt` in the tree without a commit, pushed a new
commit to the remote, and put a `git` wrapper first on `PATH` that
refuses every `git status` and runs the real git for every other
command. Then it staged the volume again, and wanted `a.txt` to keep
the change. It failed with:

```
a.txt holds "one" after a stage whose git status failed, want "unsaved" because the change was never committed
```

A second test made a volume diverge, merged its side branch on the
remote, changed `c.txt` in the tree without a commit, and staged the
volume again. `git status` worked in this test. It failed with:

```
c.txt holds "three" after the stage that returned the volume to its ref, want "unsaved" because the change was never committed
```

So the diverged path loses uncommitted changes even when `git status`
works, because `resetToUpstream` does not check for them. A damaged
index does not reproduce the first case, because `git reset --hard`
refuses a damaged index too.

The tests are not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When the driver cannot tell whether the pod has changes it has not
committed, should the stage fail so the kubelet tries again, or keep
the tree as it is and report it? And should a volume that returns to
its ref keep the changes it has not committed?

Whether a failed `pending` fails the stage, so the kubelet retries, or
leaves the tree as it is and records a fault, as `reportUpstreamMoved`
does for uncommitted changes. The diverged path needs the same
decision.
