# `lowerFirst` lowercases a message that starts with a kind

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19. It is not reproduced in a running operator.

`lowerFirst` in `observatory-operator/logs.go` puts the first letter
of a message in lowercase when the first word has no second capital
letter, so that a fragment can follow a colon. The comment on
`sentence` in the same file states that no fragment starts with the
name of a resource.

Nothing checks that statement. A resource prints as its kind and its
name, such as "Dome lab", and "Dome" has no second capital letter. So
a message of the form "%s ..." with a resource first comes out as
"dome lab ...". A rewrite of the messages in `triggers.go` and
`lateactivation.go` made such a message, two tests failed, and the
message went back to a form that starts with a lowercase word.

## The reproduction

A test builds `Dome lab` with `resource.String()` and calls
`procCall.summary` with `Dome lab is not connected`, for a procedure
call with a condition. It wants the kind to keep its capital. It
failed with:

```
summary = "When the weather is unsafe: dome lab is not connected", want "When the weather is unsafe: Dome lab is not connected"
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

A message that starts with a resource, such as `Dome lab`, comes out
as `dome lab` after a colon.

- Does `lowerFirst` leave a word alone when it is the name of a kind?
- Or do the callers pass the resource apart from the rest of the
  message, so that no function has to guess?
