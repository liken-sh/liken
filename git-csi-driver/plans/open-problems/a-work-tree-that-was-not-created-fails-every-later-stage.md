# A work tree that was not created fails every later stage

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`workTree.exists` in `worktree.go` reports whether the git directory
has a `HEAD` file. `create` writes that file in its first git step,
`git init`, and then writes the alternates file, runs
`git symbolic-ref`, and runs `git reset --hard` to check out the
commit. When any step after `git init` fails, or the driver stops
during those steps, the git directory has a `HEAD` and no checkout.
`stageTree` in `stage.go` does not remove the directory when `create`
fails.

The kubelet retries the stage. This time `exists` returns true, so
`stageTree` skips `create` and calls `head`, which runs
`git rev-parse --verify HEAD`. HEAD names a branch with no commit, so
the call fails, and `stageTree` returns `Internal`. Every later retry
takes the same path, so the volume does not stage until a person
removes `<store>/volumes/<handle>/`.

## What would confirm it

A test in `stage_test.go` that runs `git init` in a volume's git
directory and then stages the volume. The stage fails with
`Internal`, and a second stage fails the same way.

## What a fix has to decide

What marks a finished work tree. `exists` could test the last thing
`create` writes, such as the index or `pushedRef`. Or `stageTree`
could remove the volume directory when `create` fails, which is safe
only while the tree holds no work of the pod. `reclaim` in
`reclaim.go` also calls `exists`, and needs the same meaning.
