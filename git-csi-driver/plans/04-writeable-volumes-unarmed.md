# 04, Writeable volumes, unarmed

Proposed. Low fidelity.

## The problem

An application wants a directory it can write, and a person wants that
directory to become a repository. Before the driver commits anything, it
has to hold the tree correctly, refuse the access modes that break the
single-writer promise, and report what it would commit.

## The design

- The volume is a static `PersistentVolume` with the `csi` block from
  the design and a claim that names it. The driver refuses to publish a
  writeable volume under any access mode other than `ReadWriteOncePod`.
- Stage adds a work tree for the volume from the bare repository of its
  URL, or reuses the one from the last stage on this node, and checks
  the ref out. Publish bind-mounts the work tree read-write onto the
  target path.
- The watch is inotify on the work tree with a periodic `git status`
  sweep as the backstop. A write starts the quiesce timer. When it
  fires on an unarmed volume, the driver records the list of paths it
  would commit and their sizes, and commits nothing.
- The volume is unarmed until its claim names a
  `VolumeAttributesClass`. The driver learns the class by watching the
  claims bound to the `PersistentVolume` objects it has staged.
- Every state reaches the three channels: the `VolumeCondition`, an
  `Event` on the pod and the claim, and the metrics in the design.

## Considered and set aside

- **Committing without a class.** An unarmed first commit is where a
  secret or a database gets pushed. The class is the consent step.
- **Reading the class from an annotation.** A string map with no
  validation and no status. The class exists for this.

## Proof

- Unit tests run against real repositories and real directories.
- In the lab, a pod writes files, the volume's condition names them as
  pending, `RWO` and `RWX` claims are refused with a clear message, and
  a pod restart on the same node finds the same tree.
