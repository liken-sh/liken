# The smoke check of `git gc` runs no gc

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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
`collected`, is `1b5252e…`, so it is not in `objects/17/`. gc runs only
when both other objects start with `17`. In almost every run,
`gc --auto` exits 0 without starting any step, and the check passes
whether or not the exec path is correct.

## What would confirm it

The same commands in the image with `GIT_TRACE=1`. The trace shows no
`pack-refs`, `reflog`, `repack`, or `prune` child process.

## What a fix has to decide

How to make gc run. `git gc` without `--auto` always runs its steps.
If the check must exercise `--auto` as the sweep calls it, the
repository needs enough loose objects in `objects/17/`, or a pack
count above `gc.autoPackLimit`.
