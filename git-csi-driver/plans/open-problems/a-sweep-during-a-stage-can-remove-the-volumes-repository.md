# A sweep during a stage can remove the volume's repository

Open bug, recorded on 2026-11-19. A review of the sweep found it, and a
unit test reproduced it for an inline volume on 2026-11-19.

`stage` in `node.go` fetches a volume's ref into the bare repository
and checks out the commit while it holds the repository's lock. It
releases the lock when it returns. The caller adds the volume to the
node after that: `stageReadOnly` in `readonly.go` writes the record and
then adds the volume to `n.staged`, and `NodePublishVolume` in
`node.go` bind-mounts the tree, writes the record, and then adds the
volume to `n.volumes`.

`usesRepository` in `sweep.go` keeps a repository when a volume in
memory has its URL, or when the alternates file of a work tree points
to it. A read-only volume has no work tree, so a sweep that takes the
lock in the gap finds neither and deletes the repository. The volume
then joins a fetch loop for a repository that is gone, and its tree
stops following the ref.

## The reproduction

A test gave the node a `mountSyscalls` that runs a sweep at the first
bind mount of an inline publish. The fetch has then released the
repository's lock, and the volume is not yet in `n.volumes`. The test
published an inline volume and wanted its repository to be there
afterwards. It failed with:

```
the repository of the published volume is gone after a sweep that ran during the publish: stat …/store/repos/99fba95c…: no such file or directory, want it kept for the volume's fetch loop
```

The gap of `stageReadOnly` holds only the write of the record, which
has no seam, so no test reaches it without a hook in the code. The
order of the calls is the same.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should a volume count as using its repository from the start of its
stage or publish, so that a sweep never removes the repository of a
volume that is still arriving?

The lock is taken inside `stage`, so keeping the volume in the
repository's view until the caller adds it changes more than one
function. The fix can take the lock in the callers and hold it until
the volume is added, or can add the volume to the node before the
fetch, with a state that the other calls of the node do not take for
a finished stage. A sweep that also reads the records does not close
the gap, because the record is written after the lock is released.
