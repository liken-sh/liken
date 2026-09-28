# A kept work tree with no volume

## The problem

The hourly sweep removes a work tree when four things are true: no
volume on the node holds it, its last unstage is older than
`--sweep-after`, `refs/git-csi/pushed` names the tree's `HEAD`, and the
tree is not diverged. A tree with a commit that no push sent is kept.
A diverged tree is kept too, even when every commit went to its side
branch, because its commits are not on the followed ref. The sweep
names a kept tree's age in the report of the next volume that stages
the same repository.

When the `PersistentVolume` is deleted and the remote is gone too, no
volume of that repository comes. The tree stays on the node for good,
and the only record of it is one log line each time the node plugin
starts. On `liken` a person has no
shell on the node, so a person who reads that line has no plain way to
remove the tree.

The sweep does not read the remote. It compares two refs in the tree's
own git directory, so a tree whose last push sent every commit is
removed at `--sweep-after` even when the remote is gone. Only a tree
with unpushed commits, or a diverged tree, stays for good. A diverged
tree logs the same line, "the work tree holds unpushed commits", which
is true of the followed ref and not of the side branch.

## Why it matters

A work tree holds a checkout and the tree's own objects. A drill or a
test that makes writeable volumes against a scratch repository, and
then deletes the repository before the last push, leaves one tree on
each node for good. The store grows by those trees and does not shrink.

## Why the sweep keeps the tree

Unpushed commits are writes that an application made and that exist
nowhere else. The driver never deletes them on its own, by design. A
deleted `PersistentVolume` does not prove that the writes are
unwanted: a person can make a new `PersistentVolume` with the same
volume handle, and its first stage reuses the tree and pushes the
commits.

## What a fix could look like

1. **A removal a person asks for.** The driver reads a request, for
   example an annotation on the `Node` that names a volume handle, and
   removes that tree on its next sweep, with or without unpushed
   commits. The person then decides, and the driver keeps its rule.
2. **A second, longer age.** A flag such as
   `--sweep-unpushed-after` removes a kept tree after a much longer
   time, for example a year, and logs the commits it dropped. Empty
   keeps the current behavior.
3. **A recipe in the guides.** The writeable guide gives the steps to
   remove a tree by hand through an ephemeral container that shares the
   node plugin's process namespace and reads the store through
   `/proc/1/root`. This needs no code.

## What is not known

- Whether a cluster owner wants the driver to delete unpushed commits
  at all, even at a long age.
- Whether the driver can tell a remote that is gone from a remote that
  is down for a time. A fetch failure has the same form in both cases.
