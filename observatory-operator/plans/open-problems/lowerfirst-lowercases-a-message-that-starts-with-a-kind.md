# `lowerFirst` lowercases a message that starts with a kind

Open bug, recorded on 2026-11-18. It is suspected and not reproduced
in a running operator.

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

## What would confirm it

A test that passes a message that starts with a resource's `String()`
to `procCall.summary` and reads the result.

## What a fix has to decide

Whether `lowerFirst` leaves a word alone when it is the name of a
kind, or the callers pass the fragment and the resource separately so
that no function has to guess.
