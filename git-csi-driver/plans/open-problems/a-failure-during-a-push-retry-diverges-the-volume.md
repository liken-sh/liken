# A failure during a push retry diverges the volume

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

When the remote rejects a push of a writeable volume because the ref
moved, `push` in `push.go` calls `rebaseAndRetry`. That function
fetches, rebases, and pushes again, up to `rebaseAttempts` (3) times,
and returns false when it does not push. On false, `push` calls
`diverge`, and the volume pushes to its side branch until a person
merges that branch.

`rebaseAndRetry` returns false for every kind of failure, not only for
a rebase conflict or a ref that keeps moving:

- A failed `fetchUpstream`, such as a network error, returns false at
  once.
- A failed `sendTo` runs `continue` for any error. The code does not
  check `rejectedPush` there, as `push` does for the first push. A
  timeout or a failed connection uses up an attempt, and three of
  them diverge the volume.

So a remote that is unreachable for a moment, just after another
writer moved the ref, puts the volume on a side branch. The same
outage on the first push only records a push failure, and the next
push tries again.

## The reproduction

A test committed a change in an armed writeable volume, installed a
`pre-receive` hook on the remote that refuses every push to `main`,
and put a `git` wrapper first on `PATH` that refuses every `git fetch`
the way git does when the remote does not answer. Then it pushed. The
first push was refused, the retry's fetch failed, and the volume moved
to its side branch. The test wanted the volume to stay on its ref, and
failed with:

```
the volume pushes to "main.config" after a fetch that timed out, want its ref because no other writer was shown to have moved it
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When the remote stops answering during the retry, should the volume
only record a failed push that the next push tries again, and move to
its side branch only for a conflict or a ref that keeps moving?

Which failures in the retry count as "another writer moved the ref"
and lead to the side branch, and which end the retry as a push failure
that the next push tries again. A fetch failure and a push error that
is not a rejection are candidates for the second group. A failed
`reparentMetadata` and a rebase that fails for a reason other than a
conflict also end the retry with `false` today.
