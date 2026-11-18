# The discovery `ETag` does not change with the siblings

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`serveDiscovery` in `apidiscovery.go` sends the discovery document
with `buildTag`, which is the build version. The screen and audio
aspects of the document come from the sibling APIs of
display-operator and audio-operator, so the body changes when a
sibling adds a form or stops responding, and the `ETag` does not.
`document` sends `Cache-Control: no-cache`, so a client revalidates on
every read, sends `If-None-Match` with the old tag, gets 304, and keeps
the old list until `media-api` is released again. The comment on
`buildTag` states that the document "can change without a release
while its ETag stays the same".

The OpenAPI document uses the same tag. When no public base URL is
configured, `servedOpenAPI` writes the origin of the request into its
`servers` entry, so two origins get two bodies under one tag.

## Evidence

- `apidiscovery.go`, `serveDiscovery` and the OpenAPI route pass
  `e.buildTag()`.
- `bodyTag`, a SHA-256 of the body, is what the info document uses.

## What would confirm it

A test that reads the discovery document, changes the forms that the
fake display sibling lists, and reads again with the first `ETag` in
`If-None-Match`. It returns 304.

## What a fix has to decide

Whether the discovery and OpenAPI documents use `bodyTag`, as the info
document does, or send no `ETag`.
