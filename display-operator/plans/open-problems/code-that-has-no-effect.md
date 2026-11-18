# Code that has no effect

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two parts of display-operator do work that nothing reads.

- `cli/completion.go` declares `compDirectiveError = 1` beside the
  other cobra completion directives, and no code uses it. The comment
  above says "This CLI never sends Error".
- `okResponse` in `openapi.go` fills `content` with a binary string
  schema for each media type, and then, for every route that is not a
  capture route, writes an object schema over each of the same keys.
  For those routes the first loop's values are never read.

## Evidence

- `grep -rn compDirectiveError cli/` finds only the declaration.
- `openapi.go`, `okResponse`: the first loop runs for every route, and
  the second loop under `if route.kind != captureRoute` uses the same
  `types` slice.

## What would confirm it

Delete the constant and build `cli/`. Move the binary schema under the
capture case and compare the generated document with
`docs/content/docs/reference/openapi.json`; it should not change.

## What a fix has to decide

Whether the unused constant stays as a record of cobra's directive
values, which the comment gives, or goes.
