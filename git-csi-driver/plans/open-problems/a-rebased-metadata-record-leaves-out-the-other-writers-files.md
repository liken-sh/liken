# A rebased metadata record leaves out the other writer's files

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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

## What would confirm it

A test in `push_test.go` where another writer pushes a file with mode
`0600` and its record, and the volume then pushes a commit that the
remote rejects. After the retry, the remote's `metadataRef` has no
line for the file.

## What a fix has to decide

Whether `reparentMetadata` walks the rebased checkout with
`walkTreeMetadata` in place of reusing the old record tree, or merges
the remote's entries for the changed paths into the volume's record.
