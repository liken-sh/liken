---
name: testing
description: How the components of the liken repository test their code. Covers the fake clock of testing/synctest, the fakes for a network peer, the Kubernetes API server, and a device (CEC, BlueZ, PipeWire, a Denon receiver, INDI, PHD2), fixtures and table-driven tests, the coverage gate and its floors, how to reproduce a flaky test, what a test never does, and where real-hardware and real-cluster proof happens instead. Use when writing or reviewing any test in the repository, when a bug fix needs a failing test first, when a test is slow or flaky, when a test fails in CI, when a coverage floor fails or changes, when choosing how to fake time, a network peer, the API server, or a device, or when deciding where a change is proved on real hardware.
---

# Testing in the liken repository

Every test in the repository runs on a workstation and in CI with the
Go toolchain alone. A test starts no container and no cluster, opens
no connection outside the process, and waits for no real time. CI runs
each component's tests on every push. A test that pulls an image,
calls a remote provider, or sleeps makes every push slow, and makes a
failure depend on something outside the repository. Real hardware and
real clusters prove a change in the drills at the end of this skill,
not in a test.

## How to write a test

- **Write the failing test first.** Then write the code that passes
  it, and then clean up both. A test that never failed has not shown
  that it detects the fault.
- **Test outcomes, not implementation details.** Assert what a person
  or a peer observes: the status that the operator wrote, the frames
  on the CEC bus, the requests that the API server received. A test
  that checks which function ran breaks on every refactor.
- **Keep each test flat and small.** Put no branches and no error
  plumbing in the test body. Give several cases as a table with
  `t.Run`, not as a loop with conditions (`ci/recipe_test.go`).
- **Push setup into fixtures and builders.** A builder reads as the
  thing it builds. `fakeCECBus` in `equipment-operator/cecnode_test.go`
  declares a TV and a receiver in two lines. `managedObjects` in
  `bluetooth-operator/bluez_test.go` chains `adapter`, `device`, and
  `battery` into the response that BlueZ sends. `writeTree` in `ci/`
  writes a repository into `t.TempDir()`.
- **Use real objects over mocks.** Run the real client against a fake
  peer that speaks the real protocol. Use a mock only when the real
  thing is impractical to build, and make it the narrowest one that
  works.

## Fake time with `testing/synctest`

A test that waits out a backoff, a lease, a debounce, or a retry runs
in a `synctest.Test` bubble. The bubble's clock moves forward only
when every goroutine in the bubble is durably blocked. So
`time.Sleep(time.Minute)` takes no real time. `synctest.Wait()`
returns when every goroutine has settled, and the test asserts after
it. For examples, see `kubernetes/informer/one_test.go` and the
`cecnode_*_test.go` files in `equipment-operator`.

The gotchas:

- **A socket breaks the bubble.** A goroutine that reads a real TCP
  socket is not durably blocked. So the clock never moves forward, and
  each wait takes its full real time. `httptest.NewServer` and a
  listener on `127.0.0.1` both cause this. Use `net.Pipe`, whose reads
  block on channels, or the in-memory transport of
  `kubernetes/apiservertest`.
- **`net/http`'s `Transport` hangs a bubble.** When a caller closes a
  streaming body early, the `Transport` drains the body for 50
  milliseconds while it has a lock, and the bubble's clock never
  reaches the end of that time. `apiservertest` closes the connection
  first (`kubernetes/apiservertest/transport.go`).
- **Every goroutine must end.** `synctest.Test` fails when a goroutine
  blocks forever. Start each informer, watch, and loop with
  `t.Context()`, which ends before the cleanups run, or stop it in a
  cleanup.
- **Join every goroutine. Do not only cancel its context.** A
  goroutine can continue to run after its context ends. In
  `astrophotography-operator`, the solver's index watch saw a file
  deleted after the test's context ended. It then wrote into a
  `t.TempDir` while the directory's cleanup was emptying it, and the
  cleanup failed with "directory not empty". So a function that starts
  a goroutine gives the caller a way to wait for the goroutine's end,
  such as a `Done` channel or a `Shutdown` that returns after the
  goroutine returns. The test waits on it in a cleanup. `t.Cleanup`
  runs the last function registered first, so register that wait
  after the `t.TempDir` call (`startUntilCleanup` in
  `astrophotography-operator/solver/mode_test.go`).
- **Read the first failure above a "blocked goroutines remain" panic.**
  `panic: deadlock: main bubble goroutine has exited but blocked
  goroutines remain` follows a `t.Fatal` in the bubble. The test's
  function returned while the code under test was still waiting. The
  line above the panic is the cause. When no failure is above it, a
  goroutine outlived the test: join it, as the item above says.
- **One bubble for each test.** A goroutine, a channel, or a timer
  that one bubble created cannot be used from another bubble, and Go
  panics when a test tries. So a fixture that starts goroutines, such
  as an operator and its fake API server, is built inside each test's
  bubble, and tests never share a fixture of that kind. Setup with no
  goroutines, such as a parsed schema, can be shared.
- **client-go's reflector waits once after its context ends.** When a
  streaming list gets a refused connection or a `429`, the reflector
  waits out a backoff of less than a minute. So a test that stops the
  server while a reflector runs sleeps a minute in a cleanup.
- **A limit on a real wait is not a wait.** A test outside a bubble,
  such as one that runs a real process, puts a timeout on its context,
  so a broken program fails in seconds (`testTimeout` in
  `equipment-operator/assert_test.go`). Such a test never sleeps to
  let something happen.

## Reproduce a flaky test

A test that fails now and then in CI fails because of a fault in the
code or in the test, and one passing run shows neither. Run the test
many times with the race detector until it fails:

```sh
go test -race -count=200 -run '^TestName$' .
```

Raise `-count` until the failure shows, and wrap the command in
`timeout` so that a hang ends. A `library-operator` test failed once
in CI. Locally it failed again on run 25 of 2,000, and the failure
showed a goroutine that did not wait for `Shutdown`. When thousands of
runs do not fail, read the code for the window between two goroutines
before you decide that the fault is absent. After the fix, run the
same count again, and compare the failures before and after.

## Fake a network peer

- **An HTTP peer.** Serve a handler with `apiservertest.Start`. Give
  the client `server.Client()`, or the server as its
  `http.RoundTripper`, with `apiservertest.Host` as its address. Every
  metadata provider and media server in `library-operator` is faked
  this way, such as `library-operator/tmdb_test.go`. Respond with the
  provider's real responses, saved as fixtures in `testdata/` (`library-operator/testdata/`).
- **A TCP peer.** Give the client a `Dial` function, and give each
  dial one end of a `net.Pipe`, with the fake on the other end.
  `equipment-operator/denon/pipenetwork_test.go` returns
  `ECONNREFUSED` for an address where no fake listens, the way a
  closed port does.
- **Model the peer from a transcript.** Record a real device once,
  and keep the transcript in `testdata/`.
  `equipment-operator/denon/testdata/avr-x1700h.txt` contains the
  lines that a real AVR-X1700H sent, and `transcript_test.go` parses
  them. The fake receiver in `denon/fakeserver_test.go` responds to
  each query the way that receiver did.
- **The release channel and a registry.** `liken/verify/verifytest`
  serves, in the test's process, a release on a channel, and an image
  with its attestations and signature in a registry. It serves them
  from the bytes that CI published for one release. So a test of
  `liken verify`, or of a command that runs it, needs no network. A
  Sigstore bundle verifies only the bytes that it signed. So a test can
  change a file to prove that a check rejects it, but it cannot sign a
  file of its own.

## Fake the Kubernetes API server

Serve the fake through `kubernetes/apiservertest`, and point client-go
at `server.Config()`. The fake is a handler. It is a scripted handler
when the test controls each response, or a small stateful one that
stores collections. An operator that needs its own has it in
`fakeapi_test.go`, such as `observatory-operator/fakeapi_test.go`.
`liken/kubernetes/fakeapi` serves the tests of the operators of
`liken`. None of them is a model of the API server, so a test that
needs a selector scripts its handler.
A test of optimistic concurrency calls `CheckVersions` on
`liken/kubernetes/fakeapi`. After that call, the fake returns `409`
for a stale update, and keeps the version unchanged for an update that
changes nothing (`liken/cluster-operator/pausedleader_test.go`).

The `kubernetes` module has a fake beside each shared package that a
test needs, such as `kubernetes/events/eventstest`, which records each
`Event` that a component posts. The `operators` skill, under
"Conditions and Events", tells you how a test uses it.

Test the operator's handlers through the real reflector, and leave the
reflector's own faults to upstream. The `operators` skill describes,
in `references/client-go-watch.md`, the streaming list that a fake
must serve. It also lists the scenarios that a watch must pass. Do not
test a CRD schema or a CEL rule against a fake. The result depends on
the order in which the API server prunes and validates, so drill it
(see the last section).

## Fake a device

Fake a device at the narrowest interface that the code calls, and keep
the protocol above that interface real.

Look for an existing fake before you write one. A package that other
packages test against has its fake in a package named
`<package>test` beside it, such as `liken/machine/supplicanttest`
for the `wpa_supplicant` control socket. An operator's own fakes are
in files named `fake*_test.go` in its package, such as
`observatory-operator/fakejobs_test.go`, which completes each `Job`.
The doc comment of each fake says what it fakes. The entries below
are the fakes that those names do not show, and the methods to copy.

- **CEC.** `equipment-operator/cec/cectest` implements `cec.Handle`,
  the kernel's CEC ioctls, on a `cectest.Bus` in memory. For the calls
  that the repository makes, it responds the way the kernel's CEC core
  does. Scripted peers, such as a TV, respond on the bus. So a real
  `cec.Device` sends and reads real frames.
- **BlueZ.** Build the D-Bus responses that BlueZ returns, such as
  `GetManagedObjects`, with the builders in
  `bluetooth-operator/bluez_test.go`.
- **PipeWire.** Save `pw-dump` output as fixtures in
  `audio-operator/testdata/`, and give the parser the fixture as an
  `io.Reader` (`decodeGraphs` in `audio-operator/pwmonitor_test.go`).
  Some tests put a script named `pw-dump` on `PATH`, such as
  `stubPWDump`. Such a test runs a real process and waits on the wall
  clock, so it breaks the rules here. A fix needs a seam where the
  code starts a process, such as a function field that returns a fake
  command. `audio-operator/plans/open-problems/` records those tests
  while they remain.
- **Monitors.** Save each real monitor's EDID and modes in
  `display-operator/testdata/`, and parse them in the test.
- **INDI and PHD2.** `observatory-operator/indi/inditest` and
  `observatory-operator/phd2/phd2test` are fake servers on `net.Pipe`
  that replay the transcripts of a real `indiserver` and a real PHD2.
  The tests of `astrophotography-operator` import them too.
  `inditest` imports only the standard library, so that import adds
  nothing else from `observatory-operator`. Keep it that way.
- **The kubelet.** The fake API server of
  `observatory-operator/fakeapi_test.go` makes each pod that it creates
  Ready at once, unless a test keeps the pod Pending.

Some tests use a real kernel interface, such as `/dev/uinput` in
`bluetooth-operator/uinput_test.go`, or the `vivid` CEC adapters in
`equipment-operator/cecnode_vivid_test.go`. Such a test skips when the
user cannot open the interface. CI's runner has neither interface, so
the coverage floor counts only what CI runs.
`bluetooth-operator/.testcoverage.yml` lists the tests to skip on a
workstation to measure the same number.

## Where a fixture lives

- **Next to its first user.** Put a fixture or a builder directly
  above the first test that uses it, in the same file
  (`sleepingRoom` in `equipment-operator/cecnode_screen_test.go`).
- **Shared only with a second user.** A helper moves to its own test
  file when a second test file needs it.
  `equipment-operator/assert_test.go` has the assertions that every
  file uses, and `pipenetwork_test.go` has the network that every fake
  listens on.
- **Data in `testdata/`.** Real device output, provider responses, and
  golden files go in the package's `testdata/`
  (`liken/init/testdata/grubenv-editenv.golden`). The Go tools skip
  that directory when they look for packages.
- **Small, focused files.** When a test file grows, split it by
  behavior, the way `equipment-operator` splits `cecnode_*_test.go`
  into power, standby, wake, and the rest.

## The coverage gate

The `go` job in a component's `package.toml` runs the gate. In most
components, the job runs `make test-go`. Read the target in the
component's `Makefile` for its steps. It checks formatting and
`go mod tidy`, runs `go vet`, runs the tests once with `-race`, and
checks the coverage profile against `.testcoverage.yml`. It also fails
on any package with no test file (`UNTESTED_PACKAGES`), because such a
package writes nothing to the profile, and the gate would never count
it. For a new
component, copy the target and the config from a sibling component,
such as `bluetooth-operator/`.

The jobs differ in these components:

- **`kubernetes/` and `ci/`** run `make test`, which runs the same
  steps. In `kubernetes/`, it also fails when a package that small
  programs import links a large `k8s.io` package. The comments at
  `PLAIN_K8S` and `INFORMER_HEAVY` in `kubernetes/Makefile` name the
  packages and the reasons.
- **`observatory-operator`** splits its tests across shards. Every
  test of the operator's package starts the whole operator against the
  fake API server, and `-race` makes each test slow. `package.toml`
  sets `shards` and `shard-run = "make test-go-shard"`. The `go` job
  runs `make test-go-gate`, which adds up the profiles of the shards
  and checks the floor. A `lint` job runs `make test-go-lint`. On a
  workstation, `make test-go` runs the lint, the tests, and the gate
  in one process.

Set each floor in `.testcoverage.yml` about a percentage point under
the measured total. Some blocks run only when a goroutine reads state
between two events. So the total changes by a few statements from one
run to the next, and a floor at the exact total fails at random. When
a change raises the total, raise the floor to keep the gain. Lower a
floor only with the maintainer's approval, and say why in a comment at
the number, as `observatory-operator/.testcoverage.yml` does. Each
floor assumes Go 1.27.2 or later. Go 1.27.1 and earlier count a basic
block once for each piece that comments split it into
(golang/go#80974), so they report higher coverage.

## What a test never does

- Start a container, pull an image, or call `docker`.
- Start a cluster: no minikube, kind, k3d, or k3s.
- Reach a host outside the process: no metadata provider, registry,
  forge, or device on the network.
- Sleep or poll on the wall clock to let something happen.
- Depend on hardware that CI lacks without skipping when it is absent.

## Where real proof happens

A fake proves the code against what the project knows about a peer.
Real machines prove it against the real thing, by hand. A change to an
operator follows these steps:

1. Push the commit to `main`. Each push to `main` publishes a
   development build of each component that changed, so the build to
   prove is the one that CI published. The `releases` skill says how to
   pin a development build.
2. Roll the development builds out to the testbed, `liken-1`. Prove
   the change there on the real hardware that the component's own
   documentation names: a TV on the CEC bus, a controller on the
   radio, a monitor on the GPU.
3. Cut a release when the change works on `liken-1`. The steps are in
   the `release` skill.

QEMU guests prove the OS and its install path before a release of the
OS goes to `liken-1`. The drills of the CSI drivers also run on QEMU
guests:

- **The `lab` fleet.** The QEMU fleet under `liken/dev-cluster/`,
  `node-1` through `node-5`, runs upgrades, fallbacks, and an
  operator's install. The steps are in the `dev-cluster-drills` skill.
  Its `references/schema-drills.md` has the drill for a CRD schema or
  CEL change.
- **A component's own `lab/`.** `git-csi-driver/lab/` and
  `per-node-csi-driver/lab/` each boot one `liken` machine in QEMU
  from the release channel, and their drills run there.

Other checks:

- **Smoke checks.** In `package.toml`, each image can name a script
  under its component's `smoke/`. CI's image job runs that script on
  each image that it builds and loads (`mpv/smoke/mpv.sh`). To run one
  on a workstation, build the image with
  `docker buildx bake <target> --load`, and pass its reference to the
  script. This is the one place where CI runs Docker.

A container or a local cluster is fine as a temporary experiment on a
workstation, for example to measure a binary's memory. Record what it
measured in a plan, and keep its setup out of the repository.
