# The CLI has an unused parameter and an unused constant

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two names in `cli/` are declared and never read:

- `runUnpair` in `cli/unpair.go` takes `stderr io.Writer`, and its
  body does not use it. `run` in `cli/main.go` passes `stderr`, and
  `cli/unpair_test.go` passes `io.Discard`.
- `compDirectiveError` in `cli/completion.go` is declared with the
  other cobra directive values, and no code in the component returns
  it. The CLIs of `audio-operator`, `display-operator`,
  `library-operator`, `media-operator`, `observatory-operator`, and
  `liken` declare the same constant, and none of them uses it either.

## Evidence

A search of `bluetooth-operator/cli` for each name finds only the
declaration and the callers named above. `go vet` does not report an
unused parameter or an unused package-level constant.

## What would confirm it

Delete each name and build the CLI. The build and the tests pass if
nothing reads them.

## What a fix has to decide

- Whether `runUnpair` should print to `stderr`, for example when the
  `Peripheral` is not found, or drop the parameter.
- Whether `compDirectiveError` stays as part of the table of cobra
  values that every CLI copies, or leaves every copy at once.
