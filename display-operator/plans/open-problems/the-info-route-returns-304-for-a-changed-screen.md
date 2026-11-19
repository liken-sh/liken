# The info route returns 304 for a changed screen

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

The test read the info document of `HDMI-A-1` from display-api with
the fake sidecar, changed the fake to report a 3840x2160 mode, and
read the document again with the first `ETag` in `If-None-Match`. It
asserted a 200. It failed with:

```
after the mode changed to 3840x2160, a client holding the ETag "dev" got 304, want 200 with the new mode
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should the info route send no `ETag`, so a client always reads the
current screen, or an `ETag` made from a hash of the document, which
still answers 304 when nothing changed but makes a `HEAD` request
build the document?

The info route can send no `ETag`, or an `ETag` made from a hash of
the body, which still gives a client a 304 when nothing changed. The
`HEAD` path does not build a body, so a hash rules out a cheap `HEAD`.
