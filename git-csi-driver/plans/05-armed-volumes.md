# 05, Armed volumes

Proposed. Low fidelity.

## The problem

A person sets a `VolumeAttributesClass` on the claim, and from then on
the driver commits what the application writes and pushes it under the
class's policy. A restore from the last push has to give the
application its files with the right modes and its empty directories.

## The design

- The controller plugin is a `Deployment` with the `external-resizer`
  sidecar. It serves `ControllerModifyVolume`, validates the class's
  parameters, and refuses a class with an unknown or malformed
  parameter. It serves no other volume RPC.
- The class parameters are `push.quiesce`, `push.maxLatency`,
  `commit.maxFileSize`, `commit.author`, `ignore`, and `metadata`, as
  the design states. The node plugin reads the claim's current class
  and applies the policy to the running watch without a remount.
- A commit records metadata first: mode, owner, group, and every empty
  directory, on `refs/git-csi/metadata`, and only when something
  changed. Then it stages every path under the size guard, honoring the
  repository's `.gitignore` and the class's `ignore` list, and commits
  as `commit.author`. A skipped file is named in the condition.
- A push goes to the ref when `push.quiesce` or `push.maxLatency` says
  so, and always at unpublish and unstage. Credentials come from
  `nodePublishSecretRef`.
- Stage replays the metadata ref after every checkout.

## Considered and set aside

- **Policy in a file in the repository.** It puts the driver's
  configuration in the application's tree, and the person who owns the
  tree did not ask for it.
- **Metadata in the tree.** The same objection. A driver-owned ref
  never appears in the checkout or on the forge's file view.

## Proof

- Unit tests run against real repositories, with a bare repository as
  the remote.
- In the lab, a pod writes, a class is set, the commit and push land on
  the host forge inside `push.quiesce`, a changed class takes effect
  with no restart, a file over the size guard is skipped and named, and
  a pod deleted mid-timer pushes at unpublish.
