# Some failures of a tap return the wrong HTTP status

Open bug, recorded on 2026-11-18. A review of the comments against the
code found each case below, and unit tests reproduced each one on
2026-11-19. They
share one remedy: the status of a failure must say whether the caller
or the system is at fault, and whether a retry can succeed.

## A token review that did not finish is a 401 in the capture container

audio-api and the capture container both check a bearer token with a
TokenReview through `reviewer.review` in `tokenreview.go`. The error
wraps `ErrTokenDenied` when the API server refused the token, and is
another error when the review itself failed, for example when the API
server did not respond.

`apiServer.authenticate` in `apihandler.go` returns 401 for
`ErrTokenDenied` and 503 for any other error. `captureServer.authenticate`
in `capture.go` returns false for every error, and `handleRequest`
answers 401 with an `invalid_token` challenge. audio-api relays the
container's problem document through `relayProblem` in
`apiendpoints.go`, so a caller whose own token audio-api accepted
receives a 401 when the capture container could not reach the API
server.

## A failure that does not clear is a 503 with `Retry-After`

`relayFailure` in `apiendpoints.go` answers 504 for
`ErrCaptureTimeout`, 502 for `ErrCaptureMalformed`, and 503 with
`Retry-After` for any other error of `forward`. Its comment lists the
causes in the 503 branch. A refused connection and a CA that audio-api
has not minted yet clear on their own. A token file that `forward` in
`apiforward.go` cannot read, and a CA PEM that holds no certificate
(`transport`), do not clear without a change to the pod or the
`Secret`, so the caller retries a request that cannot succeed.

## A graph that does not parse is a 404

`handleRequest` in `capture.go` answers 404 `problemNoNode` for every
error of `resolveNode` in `capturegraph.go`. `resolveNode` also returns
an error when the `pw-dump` document does not parse, so output that is
not JSON reads to the caller as an endpoint that does not exist. A
failed `pw-dump` run is already a 503 a few lines above.

## The reproduction

Four tests sent one tap request each and read the status that the
caller received.

- A capture container whose TokenReview server answers 500 failed
  with: `the tap returned 401 Unauthorized, want 503 Service
  Unavailable because the API server did not answer the review`.
- A capture container whose graph read returns text that is not JSON
  failed with: `the tap returned 404 Not Found for a graph that is
  not JSON, want the status of a failed graph read`.
- audio-api with a token file that does not exist failed with: `the
  tap returned 503 Service Unavailable with Retry-After: "5", want no
  Retry-After because the token file is missing`.
- audio-api with a CA PEM that holds no certificate failed with: `the
  tap returned 503 Service Unavailable with Retry-After: "5", want no
  Retry-After because the CA holds no certificate`.

The tests are not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Which status does a caller of the capture API get in each case? For a
token review that the API server did not answer, the capture
container can answer 503, as audio-api already does. For a token file
that is missing or a CA that holds no certificate, the options are
500 with no `Retry-After`, or 503 with `Retry-After` as today. For a
graph that does not parse, the options are 502, or 503 as for a
failed `pw-dump` run. The capture container can follow
`apiServer.authenticate` for the review.
