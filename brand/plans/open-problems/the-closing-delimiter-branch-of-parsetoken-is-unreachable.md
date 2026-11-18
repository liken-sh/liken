# The closing-delimiter branch of `parseToken` is unreachable

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`parseToken` in `brand/apiref/document.go` builds the value that
starts with a token that the caller has already read from a
`json.Decoder`. A scalar becomes a scalar node, `{` starts an object,
and `[` starts an array. Every other delimiter, `}` or `]`, falls
through to the error "the document has %q where a value should
start".

`json.Decoder.Token` refuses a closing delimiter in a place where it
does not close an open object or array, and returns its own syntax
error. So `parseToken` gets `}` or `]` only where the delimiter is
valid, and the callers handle those places first:

- `parseValue` reads a value at the top level and after an object
  key. `Token` refuses `}` and `]` in both places.
- `parseArray` checks for `]` before it calls `parseToken`, and
  `Token` refuses `}` inside an array.
- `parseObject` reads keys itself and returns at its closing `}`.

If this is right, the error return is dead code, and its message never
reaches a reader. No test in `brand/apiref/document_test.go` reaches
it.

## What would confirm it

A unit test that gives `parseValue` a stray `}` or `]` at the top
level, after a key, and inside an array, and checks which error each
case returns. If every case returns the decoder's syntax error, the
branch is unreachable. The coverage profile of `make test-go` shows
whether any test runs the line.

## What a fix has to decide

Whether to remove the branch or keep it as a guard against a change
in `encoding/json`. If it stays, a test or a comment states why it
stays. If it goes, the `switch` needs some other way to end, because
Go requires a return after it.
