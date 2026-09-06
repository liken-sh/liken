# 09, Rebase before push

## The problem

Plan 06 made the side branch the driver's first answer to a push the
remote rejects. That was right for one writer per ref. It is wrong the
moment a repository has more than one writer, and the driver already
invites that: a pod can mount one directory of a repository with
`subPath`, so one repository can hold the configuration of many
workloads, each mounted into its own pod as a writeable volume on the
same ref. The first of two such writers to push wins. The second finds
the ref moved, takes its side branch, and stays there until its next
stage, which is its next pod restart. Its work is safe, but a person has
to notice the Event and merge the side branch by hand, for a change that
touched files the first writer never wrote.

The driver never fetches while a volume is mounted, so nothing else
moves the tree under a pod. The reconcile at stage rebases in place,
which is fine at stage because no pod is mounted then.

## The design

### A rejected push rebases and tries again

When the remote rejects a push as non-fast-forward, the driver fetches
the ref into the bare repository, rebases the volume's commits onto
the commit the remote now holds, and pushes again. It tries this a
bounded number of times, three, because a loss is a race with another
writer and the third loss in a row says the writers are racing hard.
After the third loss, or when the rebase or the tree update fails, the
volume takes its side branch as it does today. The side branch is the
fallback, not the first answer.

### The rebase happens beside the pod's tree, not in it

A rebase checks out the upstream commit and replays the volume's
commits on top of it. In the mounted tree that would rewrite the pod's
own files twice, and a pod with a file open, or a file it is writing,
would see the tree torn. So the rebase runs in a scratch work tree
made from the volume's own git directory with `git worktree add
--detach`. The scratch tree shares the volume's objects, so nothing is
copied. A rebase that conflicts is aborted there and the scratch tree
is removed, and the mounted tree never changed.

The stage-time reconcile uses the same scratch rebase, so the driver
has one rebase path and one set of tests for it.

### The pod's tree takes the result in one step

After the scratch rebase, the mounted tree moves from the commit it
stood on to the rebased commit with `git read-tree -m -u old new`,
then the branch ref moves to the rebased commit. `read-tree -m -u`
with two trees checks out only the paths that differ between the two
commits. For a writer whose commits touch different files than the
other writers', the pod's own files are byte-identical in both
commits, so git never rewrites them, and the only files that change
under the pod are the other writers' files. The pod's tree is then a
full, current checkout of the rebased commit, and its next push is a
plain fast-forward.

`read-tree -m -u` also refuses to overwrite a path that is dirty in
the tree and differs between the two commits. That path is one the
pod wrote since its last commit and another writer changed too, which
is the overlapping case, and the volume takes its side branch. A dirty
path that did not change between the commits is kept as it is, and it
goes into the next commit. The modes and owners of the paths the
update touched are replayed from the metadata record, as a stage does.

The rebase runs on the watcher's goroutine, the one that commits, so
no commit runs while the tree is updated.

### What the driver says

A rebase that lands posts one Event on the claim, `GitVolumeRebased`,
naming the count of commits, the upstream commit, and the ref. A
person reading the claim learns that their volume moved under them
and why. The Events for a push, a divergence, and a heal are unchanged.

### A diverged volume heals at its next push

Today a diverged volume heals at its next stage only, which is its
next pod start. A person who merges the side branch on the forge
waits for that restart, and every push in between still goes to the
side branch. Now a diverged volume's push fetches the ref first. When
upstream holds the side branch's head, the person has merged, and the
driver heals in place: the mounted tree takes upstream with the same
`take` step, the branch moves back to the ref, the side branch is
deleted on the remote, and the pending commits push to the ref. The
Event is `GitVolumeHealed`, as at stage. When upstream does not hold
the side branch's head, the push goes to the side branch as before.

### The metadata record stays one ref, and follows the same rule

The metadata record is one ref per remote, `refs/git-csi/metadata`,
and a record is a snapshot of the whole tree's modes, owners, and
empty directories as one volume sees them. Two writers on one
repository break that in two ways. After a rebase, a writer's tree
holds the other writer's files with default modes, so its next
snapshot would claim so. And each writer's push of the ref is a
non-fast-forward for the other, so the second push is rejected.

The first break is fixed at `take`. After the mounted tree moves,
the driver replays the remote record for the paths `take` rewrote,
and only those. Those paths are exactly upstream's changes, so the
other writer's modes land on the other writer's files, and the pod's
own files keep the modes it set. The writer's next snapshot is then
the whole truth: its own paths from its tree, and the other writer's
paths from their record.

The second break is fixed at the push. A push sends the branch and
the metadata ref together, and the remote answers for each. When the
metadata ref is rejected, the driver fetches the remote's record head
and makes a new record commit with the same tree and the fetched head
as its parent. A snapshot needs no textual rebase: the new parent is
the rebase, and it can never conflict. The push is then a
fast-forward. This runs inside the same retry as the branch, bounded
by the same count, and commits stay local.

Restore is unchanged: a new handle fetches the one ref as today. One
writer alone sees no change, because its pushes are never rejected.

### What stays out

`git replay` would rebase with no scratch tree at all. It is marked
experimental in git's documentation, so the plan uses `git worktree
add`, which every git has.

## The drill

Drill 08 seeds a repository with two directories, `assistant/` and
`maps/`, and runs two writer pods on one node, each with its own
writeable claim on `main` and each mounting one directory through
`subPath`. Both pods write in their own directory. Both changes reach
`main` on the forge, no side branch appears, and one claim posts
`GitVolumeRebased`. Then both pods write the same file in a shared
directory, and one of the two volumes takes its side branch. The
drill merges the side branch on the forge and writes in that pod
again, and the volume heals at that push with no restart: the side
branch is gone and the write reaches `main`. Drills 03 and 06 still
pass: drill 06's conflict is on the same file, so the
rebase there aborts and the volume diverges as before.

## What is done when

- A rejected push fetches, rebases in a scratch tree, updates the
  mounted tree with `read-tree -m -u`, and pushes again, three times
  at most, then diverges.
- The stage-time rebase runs through the same scratch path.
- A diverged volume whose side branch upstream now holds heals at its
  next push, with no pod restart.
- A dirty path that upstream also changed makes the update refuse and
  the volume diverge, and a dirty path upstream did not change is kept.
- After `take`, the remote record is replayed for the paths `take`
  rewrote and no other, and a rejected metadata ref is re-parented on
  the fetched remote head inside the push retry, so two writers'
  records never overwrite each other and `metadata` stays on.
- The unit tests prove each of those, and coverage stays at 100%.
- Drill 08 passes in the lab, and drills 03 and 06 still pass.
- A guide, "Give many applications one repository", carries the
  manifests for two writers on one repository with `subPath`, the
  rebase and the conflict case, and the metadata record.
