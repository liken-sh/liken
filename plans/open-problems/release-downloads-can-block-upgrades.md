# Bound and cancel release downloads

Open bug, medium priority. The release fetcher has one writer. A
stalled HTTP download can hold that writer indefinitely. Changing the
release target or source does not cancel that request, so later upgrades
cannot start.

## Failure path

`fetchBytes` and `fetchArtifact` in
[fetch.go](../../machine-operator/fetch.go) use `http.Get` without an
overall request deadline or cancellation context. The default transport
has some connection timeouts, but it does not bound the whole response.
A server can stop sending headers or body bytes while keeping the
connection open.

The document and artifact readers limit the number of bytes consumed.
Those limits stop an oversized download from consuming unlimited memory
or slot space. They do not limit the time spent waiting for bytes.

`Ensure` permits one active download. A changed request resets its
snapshot to `Idle`, but `busy` remains true until the old goroutine
returns. `run` then discards an obsolete result, but no code cancels
that goroutine. A stalled request can therefore block a new version or
a corrected source URL indefinitely.

A retarget does not always cause an indefinite stall. If the old
download finishes, a later reconcile pass can start the new one. Even
then, the missing cancellation causes unnecessary waiting and writes.

## Observable behavior and evidence

The download runs separately from reconciliation, so this fault does not
stop the machine heartbeat. The health conditions do show it:
`versionCondition` in [release.go](../../machine-operator/release.go)
reports `VersionConverged=False` with reason `Downloading`, which maps
to `Updating` in [phase.go](../../machine-operator/phase.go). The defect
is that this in-progress state has no time limit, and nothing recovers
automatically from a permanently stalled request.

A temporary local HTTP fixture stalled the response body and then
changed the target. The new request remained `Idle` behind the busy
fetcher. No live release service or cluster was used. Restarting the
operator ends the stuck request, but upgrades should not depend on a
person restarting it.

## Proposed safeguards

- Bound response-header and body waits. Use cancellation and either a
  suitable whole-transfer deadline, a progress deadline, or both. A
  transfer making valid progress over a slow link needs enough time.
- Cancel an obsolete download when its target or source changes. Do not
  block the reconcile pass while waiting for the writer to stop.
- Start the next writer only after the previous one has stopped. Remove
  partial files on cancellation and retry through the existing
  re-verification path for completed files.
- Keep transport failures retryable, keep the corruption hold, and
  continue streaming artifacts with bounded memory.

Coordinate these changes with
[staged-slot consistency](retargeting-overwrites-staged-releases.md).
A stopped writer can leave a partially updated slot. Stopping the
writer does not by itself make that slot safe to boot.

## Remedy scope

The fix is a set of focused reliability safeguards. Downloads should
finish or fail in bounded time, and a retarget should make progress
without an operator restart. The current API, heartbeat behavior, and
digest verification stay the same.

Timeout values require engineering judgment and tests on slow transfers.
A user-configurable timeout or retry policy would be a separate
interface decision. The unbounded wait can be fixed without one.

## Tests needed

Use local servers that stall before headers and during the body. Verify
cancellation, retry, and a changed source recovering without process
restart. Assert that reconciliation remains responsive and two writers
never overlap. Cover partial-file cleanup, reuse of verified artifacts,
slow but progressing transfers, and unchanged digest-mismatch behavior.
