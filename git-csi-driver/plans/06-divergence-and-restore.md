# 06, Divergence and restore

Proposed. Low fidelity.

## The problem

Upstream can move while a volume holds local commits, and a node can
die with a volume on it. The driver has to reconcile without merging or
losing work, and a fresh node has to restore an application from its
last push.

## The design

- At stage, with no pod on the tree: fast-forward when local is behind,
  rebase local onto upstream when they diverged, abort the rebase and
  mark the volume diverged when it conflicts.
- A push rejected as non-fast-forward, or an aborted rebase, moves the
  volume's pushes to `refs/heads/<ref>.<volumeHandle>`. The condition
  says `Diverged` and names both branches. Commits and pushes continue.
- At a later stage, when upstream contains the side branch, the volume
  fast-forwards back onto `<ref>` and the driver deletes the side
  branch.
- Restore is a new `PersistentVolume` and claim against the same URL on
  a node that has never staged it. Stage clones, checks out, replays
  metadata, and publishes.
- A sweep removes work trees that are fully pushed and have not been
  staged for a configurable age, and bare repositories no work tree or
  read-only volume uses.

## Considered and set aside

- **Merging at stage.** A merge commit the driver authored is a
  decision a person did not make.
- **Force-pushing local over upstream.** It loses work someone pushed.

## Proof

- Unit tests run every branch of the reconcile against real
  repositories.
- In the lab: push a conflicting change to the forge while a pod holds
  local commits, restart the pod, and see the side branch on the forge
  and the condition on the volume; merge on the forge, restart, and see
  the volume back on `main`. Then wipe the guest's disks, reinstall,
  and see the application's files return with their modes.
