# The CI `go` job on a cold build cache

The release workflow's gate step waits at most 10 minutes for the
`ci` run of the same commit, after the image build. The `ci` run
includes the `go` job and the `publish` job that follows it. The `go`
job runs `make test-go`, and that target builds the module three
times: `go vet`, the tests with `-race`, and the tests on the pinned
coverage toolchain. `actions/setup-go` restores the build cache by a
key made from the module files. When the key changes, every build
starts from an empty cache.

The run for c10fbe8, which changed `go.mod`, measured the cost. The
cache was not found, and the three builds took about 5 minutes with
no test running: `go vet` took 90 s, the race build took about 110 s,
and the coverage build took about 97 s. The run before it had a cache hit, and the same three
builds took less than a minute. So a commit that changes the module
files spends about half of the release gate's wait before the first
test runs.

The tests in the root package take about 70 s in each of the two runs
on a four-core machine. About 40 s of that is tests that run one at a
time, because they set package variables:

* The CEC wake and power timings (`cecWakeGuard`, `cecWakeSettle`,
  `cecWakeFresh`, `cecPowerReadEvery`, `cecPowerWindow`,
  `cecPowerSettle`), which `fastWake` and `fastPower` shorten. The
  tests in `cecnode_wake_test.go`, `cecnode_power_test.go`, and
  `cecnode_standby_test.go` take about 25 s together.
* The session timings (`sessionLiftDelay`, `sessionLiftRetry`,
  `sessionPowerWait`, `cecPowerReadWait`) and `discover`.

A test that runs with `t.Parallel` must not write a package variable,
because the other parallel tests read it at the same time.

## What a fix could look like

* Move the CEC timings onto the node workload's value, with the
  current values as the default, so a test gives each node its own
  timings and the tests can run in parallel. This changes
  `cecnode_power.go`, `cecnode_source.go`, `cecnode_standby.go`,
  `cecnode_wake.go`, and every test helper that starts a node.
* Build the tests once for both runs. The coverage run uses a pinned
  older toolchain, and the `Makefile` says why, so the two runs cannot
  share a build until the pin moves.
* Key the build cache so that a change to `go.mod` restores the
  previous cache and adds to it, instead of starting empty.

## What is not known

* How often a commit changes `go.mod` or `go.sum`. If it is rare, a
  cold run that stays under the gate may be enough.
* Which files `actions/setup-go` hashes for its key by default. The
  c10fbe8 commit changed `go.mod` and not `go.sum`, and the key
  changed.
