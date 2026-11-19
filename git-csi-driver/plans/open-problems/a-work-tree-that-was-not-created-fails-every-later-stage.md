# A work tree that was not created fails every later stage

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

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

## The reproduction

A test ran `git init` in the git directory of a volume, as a `create`
that stops after its first step leaves it, and then staged the volume.
It wanted the stage to succeed, and failed with:

```
the stage returned rpc error: code = Internal desc = git --git-dir=…/volumes/config/git --work-tree=…/volumes/config/tree rev-parse --verify --end-of-options HEAD: fatal: Needed a single revision, want the volume staged because the work tree was never created
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

What tells the driver that a work tree is finished: its index, its
`pushedRef`, or the removal of the directory when `create` fails?

What marks a finished work tree. `exists` could test the last thing
`create` writes, such as the index or `pushedRef`. Or `stageTree`
could remove the volume directory when `create` fails, which is safe
only while the tree holds no work of the pod. `reclaim` in
`reclaim.go` also calls `exists`, and needs the same meaning.
