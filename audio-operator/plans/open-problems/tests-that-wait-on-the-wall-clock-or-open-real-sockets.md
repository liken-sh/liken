# Tests that wait on the wall clock or open real sockets

Open problem, recorded on 2026-11-18. The tests below pass today. A
review of the comments against the code found that they break the
rules under "Tests run on the Go toolchain alone" in the root
`AGENTS.md`: a test waits on the fake clock of a `synctest` bubble,
never on real time, and a test of an operator talks to a fake API
server through `kubernetes/apiservertest`, whose in-memory
connections keep the bubble's clock moving where a real socket stops
it. The rules do not name outside processes or host devices, but a
shell script runs on the real clock, and a device of the host makes
the result depend on the machine. Each break makes a test slower, or
makes it fail on a busy CI runner. Nobody has measured how often they
fail.

- `capture_test.go`: the capture harness, `newCaptureHarness`, serves
  through `httptest.NewServer` on a real loopback socket and runs the
  shell scripts in `testdata/capture/bin` in place of `pw-record`,
  `pw-dump`, `flac`, and `opusenc`. Outside a bubble,
  `TestAClientThatHungUpEndsCleanly` polls the harness's log every
  10 ms for up to 3 s of wall time, and
  `TestTheContainerSendsItsHeadersBeforeTheDiscardIsOver` fails when
  the headers take more than 200 ms of wall time.
- `metrics_test.go`: `TestTheListenerServesTheRegistryAtSlashMetrics`
  and `TestTheMetricsListenerStopsWithTheRun` listen on
  `127.0.0.1:0` and wait up to 20 s of wall time.
- `graphprobe_test.go`: the tests put a shell script named `pw-dump`
  on `PATH`, and `TestCheckGraphReportsAPWDumpThatHangs` runs one that
  sleeps for 30 s, with a 100 ms context timeout on the real clock.
- `tokenreview_test.go`, `authorize_test.go`, and
  `apiclientca_test.go` start their fake API servers with
  `httptest.NewServer`, not with `kubernetes/apiservertest`.
- `mixer_test.go`: `TestMixerReadsTheLocalCard` opens
  `/dev/snd/controlC0` on the machine that runs the test. It skips
  when the file is missing or does not open, so it passes on CI and
  tests the hardware of a workstation.

## What would confirm it

Each break is in the test's code. To measure the cost, run
`go test -count=20 ./...` in `audio-operator/` on a loaded machine and
count the failures and the time of each test.

## What a fix has to decide

How to fake the processes. The tap runs `pw-record` and an encoder as
real processes, so a test in a bubble needs a seam where the code
starts a process, such as a function field that returns a fake
command. The metrics listener and the fake API servers can move to
`kubernetes/apiservertest`, whose connections work inside a bubble.
`TestMixerReadsTheLocalCard` either moves to the hardware proof on the
`lab` fleet or is deleted, because the fixtures of `mixer_test.go`
already test the parser.
