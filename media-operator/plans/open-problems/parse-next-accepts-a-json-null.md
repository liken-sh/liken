# `parseNext` accepts a JSON `null`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`parseNext` in `upnext.go` reads `MEDIA_NEXT` in the command sidecar
and in the blocks server. Its comment says it returns nil when the
value "is not one JSON object". It decodes the value into a `PlayNext`
struct, and `json.Unmarshal` of `null` into a struct succeeds and
changes nothing, so `parseNext("null")` returns `null`. `sendNext` then
sends the string `null` to the display as the next block.

The operator does not write `null`: `nextBlock` in `pod.go` returns an
empty string for a Play with no next block, and the pod builder sets
no `MEDIA_NEXT` then. The value reaches the sidecar only from a pod
that someone else built or edited.

## Evidence

`upnext_test.go`, `TestParseNextReadsOneObject`, has rows for an empty
value, a value that does not decode, and an array, and no row for
`null`.

## What would confirm it

A row for `null` in `TestParseNextReadsOneObject`, with the result
empty.

## What a fix has to decide

Whether `parseNext` refuses `null` too, or whether the comment says
that it accepts it. The display's handling of a `null` block decides
how much the difference matters.
