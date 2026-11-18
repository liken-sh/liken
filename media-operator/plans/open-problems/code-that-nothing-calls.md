# Code that nothing calls

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

These functions and branches in `media-operator` have no caller
outside their own tests, or cannot run.

- `terminalPhase` in `api.go`: only `api_test.go` calls it.
- `mountsIPC` in `pod_test.go`: a test helper that no test calls.
- `focusPlay` in `focus_test.go`: a test helper that no test calls.
- `Parse`, `ParseSegment`, `ReadFile`, and `SegmentPath` in
  `edl/format.go`: `ParseSegment` is called by `Parse`, and no code in
  the repository outside `edl`'s own tests calls any of the four.
  `album.go` and `local/edl` use the package's other functions.
- `minute` in `display/src/clock.rs`: only its own test calls it.
- `actionRefuse` in `cli/version.go`: `decideVersionAction` returns it
  only for a non-empty minimum CLI version, and its one caller,
  `runCapture` in `cli/capture.go`, passes `""`. So the `actionRefuse`
  case in `runCapture` cannot run. The same function exists in the
  CLIs of other components.
- `compDirectiveError` in `cli/completion.go`: declared with the other
  cobra directive values, and nothing returns it. The same constant is
  unused in the CLIs of other components.

## Evidence

A search of the repository for each name finds only the declaration,
the tests named above, and, for `actionRefuse`, the one call with an
empty minimum.

## What would confirm it

Delete each name, or the branch, and build and test the component.
The build passes if nothing else reads them.

## What a fix has to decide

For each name, whether it goes, or whether a caller is missing. The
version check and the completion constants are copies of code in the
other CLIs, so a decision on them covers every copy at once.
