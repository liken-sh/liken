# Code that nothing calls, and code kept in two copies

Open problem, recorded on 2026-11-18. Nothing fails because of it. A
review of the comments against the code found these, and a search of
`audio-operator/` for each identifier confirmed that nothing outside a
test uses it. Dead code still has to be read, and a second copy can
change without the first.

## Code with no caller

- `awaitCodec` in `codecs.go`, a method of `draPlugin`.
- `endedWords` in `tap.go`.
- `compDirectiveError` in `cli/completion.go`, a cobra
  `ShellCompDirective` value that the CLI never prints.
- `ctlEventMaskInfo`, `ctlEventMaskAdd`, and `ctlEventMaskTLV` in
  `ctlevents.go`. They name the kernel's event mask bits.
- The third parameter of `linkHeader` in `links.go`, a
  `representation` that the function ignores. Each caller passes
  `representation{}`.
- `actionRefuse` in `cli/version.go`. `decideVersionAction` returns it
  only for a nonempty minimum version, and `runCapture` in
  `cli/capture.go` passes `""`. The usage text of `--force` in
  `cli/main.go` says "run despite a version mismatch", although a
  mismatch only prints a warning that `--force` suppresses.
- `CAPTURE_STDBUF`, the variable `stdbufVariable` in `encoders.go`.
  `encoderEnvironment` reads it, and only `encoders_test.go` sets it.
  No manifest or Dockerfile sets it.

## Code that only tests call

- `expandAspectTemplate` in `fragments.go`, which `fragments_test.go`
  calls.
- `controlEvent.valueChanged` and `controlEvent.removed` in
  `ctlevents.go`, which `ctlevents_test.go` calls.

`relayBlueZSignals` in `bluez.go` has a caller, but no test calls it.
It takes a channel of signals and a `release` function in place of
the D-Bus connection, so a test could drive it without a bus, and an
earlier comment gave that as the reason for the separate function.

## Code in two copies

- `runningStale` in `declarationloaded.go` and the `stale` closure in
  `runPipewire` in `restarts.go` have the same body: each calls
  `declarationNewer` on the drop-in and `socketPath`, and treats an
  error as not stale.
- `reconcileSink` and `reconcileSource` in `sinkcontrol.go` repeat one
  body with a different kind. The `Sink` version also calls
  `noteAsk` and writes `e.latest`, which only volume asks read, and
  only a `Sink` has a volume ask.
- `wavSampleBits` in `wav.go` and `sampleBytes` in `fragments.go` are
  separate constants that must agree: 16 bits and 2 bytes.

## What a fix has to decide

For each item, delete it, move it into the test file, or add the
caller or the test that it was written for. `compDirectiveError` and
the mask bits document a format that the code reads, so a fix may
keep them as documentation. The `CAPTURE_STDBUF` override may be meant
for an image that puts `libstdbuf.so` at another path. The
`--force` text and `actionRefuse` change together, because the flag
documents a refusal that cannot happen.
