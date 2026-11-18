# The header deadline of a tap grows with its begin offset

Open question, recorded on 2026-11-18. It is suspected and not
reproduced. A review of the comments against the code found that the
reason in the comment may not hold.

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

## What would confirm it

A test of `forward` with a fake container that sends no headers, and
a begin offset of 60 s, in a `synctest` bubble. It shows when the 504
arrives.

## What a fix has to decide

Whether the offset leaves the deadline, or a case exists in which the
container waits for audio before it sends headers. `confirm` waits for
the link to the node before the headers, so a slow link is a
candidate, but that wait does not depend on the offset.
