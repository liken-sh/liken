# A nickname of only spaces panics the operator

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`initials` in `people-operator/initials.go` picks the letters that
`drawInitials` draws for a `Person`. When `spec.nickname` is not
empty, it takes the first word of the nickname:

```go
words = strings.Fields(spec.Nickname)[:1]
```

`strings.Fields` returns an empty slice for a string of only
whitespace, and the slice expression `[:1]` on an empty slice panics.
The CRD in `deploy/people-crd.yaml` sets only `minLength: 1` on
`spec.nickname`, so the API server accepts a nickname of `" "`.

`composeStatus` in `status.go` calls `drawInitials` when a `Person` has
no `spec.avatar` and when a read fails with no earlier picture. The
pass runs in the operator's process, and the code has no `recover`, so
one such `Person` stops the operator. The pod restarts, reaches the
same `Person`, and stops again, until someone edits the nickname. A
`Person` that the pass would reach after it gets no status write.

The tests in `initials_test.go` give no case with a nickname of only
whitespace.

## What would confirm it

A case in `initials_test.go` with `personSpec{DisplayName: "Ada
Lovelace", Nickname: " "}`. If the bug is real, the test panics.

## What a fix has to decide

Whether `initials` falls back to the display name when the nickname
has no word, or the CRD refuses such a nickname with a `pattern` or a
CEL rule, or both. A CRD rule alone leaves the operator open to an
object that was stored before the rule. The same question applies to a
`displayName` of only whitespace, which gives no letters, so the
thumbnail has the colour and no letters.
