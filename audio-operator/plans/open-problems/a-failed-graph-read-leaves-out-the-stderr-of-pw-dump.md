# A failed graph read leaves out the `stderr` of `pw-dump`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`readGraph` in `audio-operator/pipewire.go` runs `pw-dump` with
`command.Output()` and wraps a failure as `running pw-dump: %w`.
`Output` saves `stderr` in the `*exec.ExitError` when `Stderr` is not
set, but the error's text is only the exit status, so the wrapped
error reads `running pw-dump: exit status 1` and holds none of the
text that `pw-dump` printed. This breaks the rule "Errors include their
source's text" in `audio-operator/AGENTS.md`.

Other runs of `pw-dump` in the operator follow the rule. `dumpGraph`
in `tap.go` sets `Stderr` and adds `unexpectedStderr` to the error,
and `TestCheckGraphReportsTheFailureWithoutTheGraph` in
`graphprobe_test.go` checks that the probe's report includes
`pw-dump`'s `stderr`.

## What would confirm it

A unit test of `readGraph` with a `pw-dump` stub on `PATH` that prints
a line to `stderr` and exits 1, as `stubProbeDump` in
`graphprobe_test.go` does. The error does not include the line.

## What a fix has to decide

Nothing beyond the form, which `dumpGraph` already uses.
