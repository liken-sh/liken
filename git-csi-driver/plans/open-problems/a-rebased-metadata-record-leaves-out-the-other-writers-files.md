# A rebased metadata record leaves out the other writer's files

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

After a rejected push, `rebaseAndRetry` in `push.go` rebases the
volume's commits onto the remote's ref and then calls
`reparentMetadata` in `metadata.go`. `reparentMetadata` commits the
volume's own record tree again, with the remote's record commit as
its parent. Its comment says "each record lists the whole tree, so a
rebase only changes the parent".

The volume's record lists the tree as it was when the driver last
committed, before the rebase. The rebase adds the other writer's
commits, and `restoreChangedMetadata` in `reconcile.go` sets their
modes in the checkout from the remote's record. The record that the
push then sends still lists only the volume's own tree. A file that
the other writer added with mode `0600` has no line in it, and a mode
that the other writer changed has the volume's old value.

`recordMetadata` walks the tree again at the next commit and writes a
correct record. Until then, a stage on another node replays the
pushed record, and that file gets the default mode of a checkout.

## The reproduction

A test gave an armed volume a commit and a metadata record, then
pushed a commit that adds `secret.yaml` to the remote, with a record
that gives the file the mode `0600`. Then the volume pushed: the
remote refused the first push, and the retry rebased and pushed. The
test wanted the remote's record to keep the line for `secret.yaml`,
and failed with:

```
the remote's record after the rebase is "0664 1000 1000 a.txt\n0600 1000 1000 one.yaml\n", want the line "0600 1000 1000 secret.yaml" that the other writer recorded
```

The record lists only the paths whose mode or owner is not the
default, not the whole tree as the comment on `reparentMetadata` says.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

After a rebase, should the driver record the modes again from the
checkout, or merge the other writer's record into its own?

Whether `reparentMetadata` walks the rebased checkout with
`walkTreeMetadata` in place of reusing the old record tree, or merges
the remote's entries for the changed paths into the volume's record.
