# Some failures of a tap return the wrong HTTP status

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found each case below. They
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

## What would confirm it

Unit tests through the capture harness in `capture_test.go` and the API
harness in `api_test.go`: a review server that returns a 500, a token
file that is missing, an `audio-api-tls` `Secret` whose CA PEM holds
no certificate, and a
`pw-dump` stub that prints text that is not JSON. Each test reads the
status that the caller receives.

## What a fix has to decide

The status for each case. The capture container can follow
`apiServer.authenticate` and return 503 for a review that did not
finish. A permanent fault of audio-api's own configuration may be a
500 with no `Retry-After`. A graph that does not parse may be a 502 or
a 503, the same as a failed `pw-dump` run.
