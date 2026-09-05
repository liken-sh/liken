# 06, Divergence and restore

Proposed. Full fidelity.

## The problem

Upstream can move while a volume holds local commits, and a node can
die with a volume on it. The driver has to reconcile without merging or
losing work, and a fresh node has to restore an application from its
last push.

## The design

### Reconcile at stage

Stage is the one moment no pod holds the tree, so it is the one moment
the tree may change under the driver. After the fetch, the driver
compares the local branch to the upstream ref:

| Local against upstream | Action |
|---|---|
| Equal | Nothing. |
| Behind | `git reset --hard` the local branch to upstream. Replay metadata. |
| Ahead | Nothing. The next push carries it. |
| Diverged | `git rebase` local onto upstream. On success, replay metadata. On conflict, `git rebase --abort`, keep local, and mark the volume diverged. |

A stage that finds the ref deleted upstream keeps local, marks the
condition abnormal, and pushes nothing until the ref exists again.

### The side branch

A push rejected as non-fast-forward, or a rebase aborted at stage,
moves the volume to `refs/heads/<ref>.<volumeHandle>`. From then on
every push goes there, and the condition says `Diverged` with both
branch names. Commits continue, so the application loses nothing.

At every later stage, the driver checks whether upstream `<ref>` now
contains the side branch's tip. When it does, a person merged it, and
the driver resets local to upstream, replays metadata, deletes the side
branch on the remote, and clears the condition. When it does not, the
volume stays on the side branch.

### Restore

A `PersistentVolume` and claim against a URL, staged on a node that has
never held the volume, is a restore. Stage clones the bare repository
if the store has none, creates the work tree from the ref, replays
`refs/git-csi/metadata` when it exists, and publishes. Nothing in the
driver distinguishes a restore from a first install, which is the point.

### The sweep

The node plugin never learns that a `PersistentVolume` was deleted, so
the store grows until something removes what nothing stages any more.
Once an hour the driver walks `<store>/volumes/` and removes a work
tree that is not published, has no unpushed commit, and was last
unstaged more than `--sweep-after` ago, default 30 days. It then removes
a bare repository no work tree and no published read-only volume names.
A work tree with an unpushed commit is never removed, and its age is
reported in the condition of the next volume that stages the same URL,
so a person learns about it.

### Reporting

- The condition says `Diverged` with both branches, `RefDeleted` when
  upstream has no ref, and is normal after a heal.
- Events on the pod and the claim: `Diverged`, `Healed`, `Swept`.
- The metric `git_csi_diverged` from the design.

## Considered and set aside

- **Merging at stage.** A merge commit the driver authored is a
  decision a person did not make.
- **Force-pushing local over upstream.** It loses work someone pushed.
- **Deleting a work tree at unstage.** The next stage on the same node
  would clone again, and a node that restarts every pod at boot would
  clone every volume every boot.

## Proof

- Unit tests run every row of the reconcile table against real
  repositories, plus the side branch, the heal, the deleted ref, and
  the sweep's three conditions.
- In the lab, `lab/drills/06.sh`: push a conflicting change to the
  forge while a pod holds local commits, restart the pod, and see the
  side branch on the forge and the condition on the volume; merge on the
  forge, restart, and see the volume back on `main` and the side branch
  gone. Then `make -C lab clean install run` for a fresh guest, apply
  the same `PersistentVolume` and claim, and see the application's files
  return with their modes and empty directories.
