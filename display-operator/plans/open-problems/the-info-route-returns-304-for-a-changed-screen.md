# The info route returns 304 for a changed screen

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

display-api serves three JSON documents through `document` in
`api.go`: the discovery document, the OpenAPI document, and the info
document of one `Display`. `document` sets the build version as the
`ETag` of each, with `Cache-Control: no-cache`, and returns 304 when
`If-None-Match` matches. The discovery and OpenAPI documents change
only with a build, so the version identifies them. The info document
holds the screen's mode, size, and state from the capture sidecar, or
from the `Display` when the sidecar cannot be reached, and those
change at run time. A browser or a cache that revalidates the info
document gets a 304 and keeps a mode or a state that is no longer
true, until display-api is upgraded.

## Evidence

- `api.go`, `document`: builds the tag from `version` alone, the same
  for every route that calls it.
- `api.go`, `serveInfo`: calls `document` with the body that
  `screenInfo` built for this request.
- `routes.go`, the comment on the route kinds, gives the `ETag` and
  the 304 only to a document route. An info route is a separate kind.

## What would confirm it

A test in `api_test.go` that reads the info document, changes the
mode that the fake sidecar reports, and sends the first `ETag` back in
`If-None-Match`. The response should be a 200 with the new mode.

## What a fix has to decide

The info route can send no `ETag`, or an `ETag` made from a hash of
the body, which still gives a client a 304 when nothing changed. The
`HEAD` path does not build a body, so a hash rules out a cheap `HEAD`.
