# The header deadline of a tap grows with its begin offset

Open question, recorded on 2026-11-18. A review of the comments
against the code found that the reason in the comment may not hold,
and a unit test reproduced the longer wait on 2026-11-19.

`forward` in `audio-operator/apiforward.go` cancels a request to the
capture container when the response headers do not arrive within
`headerTimeout`, 10 s, plus the tap's begin offset. The comment says
that the offset is added so that a tap that starts with a discard is
not cut off before the container responds.

The capture container sends its headers before the discard ends.
`stream` in `tap.go` sends them after `confirm` and before the first
sample of the span, and
`TestTheContainerSendsItsHeadersBeforeTheDiscardIsOver` in
`capture_test.go` checks it. So the offset does not delay the headers,
and it only lengthens the wait for a container that sends none. A tap
with the largest offset that the API accepts, `captureBeginMax`, 60 s,
waits 70 s before the caller gets a 504.

## The reproduction

The test called `forward` in a `synctest` bubble, through a fake
container that accepts the request and sends no headers until the
request is cancelled, with a begin offset of `captureBeginMax`. It
asserted that `forward` returns `ErrCaptureTimeout` after
`headerTimeout`, and it failed with:

    forward waited 1m10s for the headers of a tap that begins at 1m0s, want 10s

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should a capture container that sends no headers fail after 10 s for
every tap, or after 10 s plus the begin offset, as today? Keeping the
offset needs a case in which the container waits for audio before it
sends headers. `confirm` waits for
the link to the node before the headers, so a slow link is a
candidate, but that wait does not depend on the offset.
