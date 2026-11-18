# A failure during a push retry diverges the volume

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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

## What would confirm it

A test in `push_test.go` beside
`TestThreeRejectedPushesInARowDivergeToTheSideBranch`: the remote
rejects the first push, and then the remote's URL fails to connect
for the retries. The volume diverges, where a push failure was
expected.

## What a fix has to decide

Which failures in the retry count as "another writer moved the ref"
and lead to the side branch, and which end the retry as a push failure
that the next push tries again. A fetch failure and a push error that
is not a rejection are candidates for the second group.
