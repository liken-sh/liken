# The smoke check of `git gc` runs no gc

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and `GIT_TRACE` confirmed it on a workstation on
2026-11-19, outside the image. No unit test can show it, because the
check runs in the image: a run of the smoke check with `GIT_TRACE=1`
in CI's image job, or on a workstation after
`docker buildx bake git-csi-driver --load`, does.

The last block of `smoke/git-csi-driver.sh` checks that the image can
run `git gc`, because the sweep runs `git gc --auto` and gc starts
each of its steps as a separate program from git's exec path. The
check makes one commit in a new repository and runs
`git -c gc.auto=1 gc --auto`. Its comment says that `gc.auto=1` sets
the loose-object threshold as low as it goes.

`git gc --auto` does its work only when it finds too many loose
objects or packs. git counts the loose objects in one directory,
`objects/17/`, and runs gc when that count is more than
`gc.auto / 256`, rounded up, which is 1. One commit writes three
objects: a blob, a tree, and a commit. The blob of the file's content,
`collected`, is `1b5252e…`, and the tree of the one file is always
`7bd6a2e…`, so neither is in `objects/17/`. Only the commit's hash
changes from run to run, and one object cannot pass the threshold. So
`gc --auto` never starts a step, and the check passes whether or not
the exec path is correct.

## What would confirm it

The same commands in the image with `GIT_TRACE=1`. The trace shows no
`pack-refs`, `reflog`, `repack`, or `prune` child process.

## What a fix has to decide

How to make gc run. `git gc` without `--auto` always runs its steps.
If the check must exercise `--auto` as the sweep calls it, the
repository needs enough loose objects in `objects/17/`, or a pack
count above `gc.autoPackLimit`.
