# A `HEAD` request for a problem gets no `Content-Length`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`audio-operator/problems.go` writes JSON responses in two functions
with nearly the same body. `writeJSON` sets `Content-Length` before it
checks the method, so a `HEAD` response has the length that a `GET`
would have, as RFC 9110 section 9.3.2 allows. `writeProblem` returns
for `HEAD` before it sets `Content-Length`, so a `HEAD` request that
fails gets a problem's status and `Content-Type` with no length. Both
the API and the capture container write problems through
`writeProblem`.

## What would confirm it

A unit test that sends `HEAD` for a `Sink` that does not exist and
reads the `Content-Length` header of the response, beside the same
test for the discovery document, which `writeJSON` serves.

## What a fix has to decide

Whether a `HEAD` response carries the length in both functions, and
whether the functions become one, since they differ only in the
default `Content-Type` and in this order.
