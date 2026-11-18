# No test reaches the conflict retry of `clearIfHeld`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When a leader steps down, `Lead.end` in `kubernetes/election/election.go`
waits for client-go's elector to release the `Lease`, and then calls
`clearIfHeld`. After a conflict, `clearIfHeld` reads the `Lease` again
and tries up to three times. Its comment gives the reason: a renewal
of this process that the API server committed after the elector
stopped makes the release conflict. client-go v0.37.2 now retries its
own release after a conflict, so in that case the `Lease` is already
released when `clearIfHeld` reads it, and `clearIfHeld` returns
without a write. The test that names this case passes through
client-go's retry and does not reach the retry loop of `clearIfHeld`.

## Evidence

- `tryRelease` in client-go v0.37.2,
  `tools/leaderelection/leaderelection.go`, calls itself again when
  `Lock.Update` returns a conflict.
- `TestAStepDownReleasesTheLeaseAfterALateRenewal` in
  `election/election_test.go` calls `RewriteAfterNextRead` once and
  then `StepDown`. The rewrite applies to the first read, which is
  client-go's release. client-go's update conflicts, its second try
  succeeds, and `mayClear` then returns false for the empty holder.
- `TestTheReleaseClearsAnExpiredLeaseOfAnEarlierProcessOfThisPod` and
  `TestTheReleaseOfALeaseItCannotReadWritesNothing` call
  `ClearIfHeld` directly, and neither one makes an update conflict.
- Completed plan 11 of `equipment-operator` records that the late
  renewal test failed with `clearIfHeld` removed, with the client-go
  of that time.

## What would confirm it

Run `TestAStepDownReleasesTheLeaseAfterALateRenewal` with the call to
`clearIfHeld` in `end` removed, and read the coverage profile of
`election` for the lines after `apierrors.IsConflict` in
`clearIfHeld`.

## What a fix has to decide

- Whether `clearIfHeld` still needs its conflict retry. It is still
  needed for an expired `Lease` of an earlier process of this pod,
  which client-go does not release.
- Which test makes the update of `clearIfHeld` itself conflict, for
  example a fake server that rewrites the `Lease` after the second
  read, if the retry stays.
- The comment of `clearIfHeld` and the late renewal test change with
  that decision.
