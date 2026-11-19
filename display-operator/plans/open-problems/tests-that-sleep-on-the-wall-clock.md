# Tests that sleep on the wall clock

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: `grep -n "time.Sleep\|time.After(" display-operator/*_test.go`
finds each sleep and poll below, at `arrivals_test.go:86`, `:206`,
`:228`, and `:249`, `weston_test.go:369` and `:384`,
`clientcerts_test.go:364`, `serving_test.go:166` and `:182`,
`layoutlink_test.go:195` and `:213`, `wayland_test.go:354`, `:370`,
`:664`, and `:795`, `watchserver_test.go:341`, and
`layoutstore_test.go:70`. No unit test can fail on a test that sleeps.
How often they fail on a loaded runner is not measured.

The root `AGENTS.md` and the `testing` skill say that a test waits on
the fake clock of a `synctest` bubble, and never sleeps or polls on
the wall clock to let something happen. A timeout that bounds a real
wait is allowed. Several tests of display-operator run outside a
bubble and sleep or poll so that a goroutine, a file, or a socket can
get ahead. Each one makes CI slower, and each can fail on a loaded
runner.

## Evidence

A scan of each `_test.go` file for `time.Sleep` and `time.After` in a
function that does not call `synctest.Test` finds these sleeps and
polls:

- `arrivals_test.go`: a 50 ms sleep in
  `TestReadyForgetsAnEarlierArrival`,
  `TestTheWatchConnectsWhenTheSocketArrives`,
  `TestTheWatchConnectsWhenAStaleSocketIsReplaced`, and
  `TestTheWatchConnectsWhenTheDirectoryArrivesLater`.
- `weston_test.go`: a 50 ms sleep before the file appears in
  `TestWaitForFileReturnsWhenTheFileAppears` and
  `TestWaitForFileReturnsWhenTheDirectoryAppearsLater`.
- `clientcerts_test.go`: a 50 ms sleep in
  `TestTheWatchKeepsTheAnchorsWhenTheConfigMapGoes`.
- `serving_test.go`: a sleep and a 10 ms poll in
  `TestTheSidecarLoadsTheLeafWhenTheVolumeIsRenamed`.
- `layoutlink_test.go`: a 50 ms sleep in
  `TestLayoutLinkDialsWhenTheModulesSocketAppears`, and the 1 ms poll
  of `waitUntil`.
- `wayland_test.go`: the 10 ms polls of `awaitServed`, `awaitMode`,
  and `TestAConnectionOpensTheCardGateAndRunsTheConnectedHook`, a
  50 ms sleep in `TestTheReadbackEndsOnTheWatchsReport`, and the
  100 ms wait of `expectNoReport`.
- `watchserver_test.go`: the 5 ms poll of `eventually`.
- `layoutstore_test.go`: the 100 ms wait of `drainWakes`.

The scan is by function, so a helper that a bubble calls can appear
in the list, and a sleep in a test that the scan missed can be absent.

## What would confirm it

Read each test above and check whether a bubble encloses it through
its caller. Run `go test -count=50` on the package under load and
count the failures.

## What a fix has to decide

Several of these tests read inotify or a Unix socket, and the
`testing` skill notes that a real socket breaks a bubble. For each
test the fix decides whether the peer can be an in-memory fake, such
as `net.Pipe`, or whether the test moves to a bubble with a fake file
watch, or whether it stays on the real peer and waits for an event in
place of a sleep.
