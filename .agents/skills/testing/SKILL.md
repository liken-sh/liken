---
name: testing
description: How the components of the liken repository test their code. Covers the fake clock of testing/synctest, the fakes for a network peer, the Kubernetes API server, and a device (CEC, BlueZ, PipeWire, a Denon receiver), fixtures and table-driven tests, the coverage gate of make test-go, what a test never does, and where real-hardware and real-cluster proof happens instead. Use when writing or reviewing any test in the repository, when choosing how to fake time, a network peer, the API server, or a device, or when deciding where a change is proved on real hardware.
---

# Testing in the liken repository

Every test in the repository runs on a workstation and in CI with the
Go toolchain alone. A test starts no container, no cluster, and no
connection outside the process, and it waits for no real time. CI
runs each component's tests on every push, so a test that pulls an
image, calls a remote provider, or sleeps makes every push slow and
makes a failure depend on something outside the repository. Real
hardware and real clusters prove a change in the drills described at
the end of this skill, not in a test.

## How to write a test

- **Write the failing test first.** Then write the code that passes
  it, then clean up both. A test that never failed has not shown that
  it detects the fault.
- **Test outcomes, not implementation details.** Assert what a person
  or a peer observes: the status the operator wrote, the frames on the
  CEC bus, the requests the API server received. A test that checks
  which function ran breaks on every refactor.
- **Keep each test flat and small.** No branches and no error
  plumbing in the test body. Give several cases as a table with
  `t.Run`, not as a loop with conditions (`ci/recipe_test.go`).
- **Push setup into fixtures and builders.** A builder reads as the
  thing it builds: `cecRoom` in `equipment-operator/cecnode_test.go`
  declares a TV and a receiver in two lines, and `managedObjects` in
  `bluetooth-operator/bluez_test.go` chains `adapter`, `device`, and
  `battery` into a BlueZ answer. `writeTree` in `ci/` writes a
  repository into `t.TempDir()`.
- **Use real objects over mocks.** Run the real client against a fake
  peer that speaks the real protocol. Reach for a mock only when the
  real thing is impractical to build, and make it the narrowest one
  that works.

## Fake time with `testing/synctest`

A test that waits out a backoff, a lease, a debounce, or a retry runs
in a `synctest.Test` bubble. The bubble's clock advances when every
goroutine in it is durably blocked, so `time.Sleep(time.Minute)` takes
no real time. `synctest.Wait()` returns when every goroutine has
settled, and the test asserts after it. See
`kubernetes/informer/one_test.go` and the `cecnode_*_test.go` files in
`equipment-operator`.

The gotchas:

- **A socket breaks the bubble.** A goroutine that reads a real TCP
  socket is not durably blocked, so the clock never advances and each
  wait takes its full real time. `httptest.NewServer` and a listener
  on `127.0.0.1` both do this. Use `net.Pipe`, whose reads block on
  channels, or the in-memory transport of `kubernetes/apiservertest`.
- **`net/http`'s `Transport` hangs a bubble.** When a caller closes a
  streaming body early, the `Transport` drains it for 50 milliseconds
  behind a lock, and the clock never gets there. `apiservertest`
  closes the connection first (`kubernetes/apiservertest/transport.go`).
- **Every goroutine must end.** `synctest.Test` fails when a goroutine
  blocks forever. Start each informer, watch, and loop with
  `t.Context()`, which ends before the cleanups run, or stop it in a
  cleanup.
- **client-go's reflector waits once after its context ends.** After a
  streaming list meets a refused connection or a `429`, it waits out a
  backoff of less than a minute. A test that takes the server down
  while a reflector runs sleeps a minute in a cleanup.
- **A bound on a real wait is not a wait.** A test outside a bubble,
  such as one that runs a real process, puts a timeout on its context
  so a broken program fails in seconds (`testTimeout` in
  `equipment-operator/assert_test.go`). It never sleeps to let
  something happen.

## Fake a network peer

- **An HTTP peer.** Serve a handler with `apiservertest.Start`, and
  give the client `server.Client()` or the server as its
  `http.RoundTripper`, with `apiservertest.Host` as its address. Every
  metadata provider and media server in `library-operator` is faked
  this way: `tmdb_test.go`, `peertube_test.go`, and
  `fakejellyfin_test.go`. Answer from the provider's real responses,
  saved as fixtures in `testdata/` (`library-operator/testdata/`).
- **A TCP peer.** Give the client a `Dial` function, and hand each dial
  one end of a `net.Pipe` with the fake on the other end.
  `equipment-operator/denon/pipenetwork_test.go` answers
  `ECONNREFUSED` for an address no fake listens on, the way a closed
  port does.
- **Model the peer from a transcript.** Record a real device once,
  and keep the transcript in `testdata/`.
  `equipment-operator/denon/testdata/avr-x1700h.txt` holds the lines a
  real AVR-X1700H sent, and `transcript_test.go` parses them. The fake
  receiver in `denon/fakeserver_test.go` answers each query the way
  that receiver answered it.

## Fake the Kubernetes API server

Serve the fake through `kubernetes/apiservertest` and point client-go
at `server.Config()`. The fake is a handler: a scripted one when the
test controls each answer, or a small stateful one that holds
collections. `people-operator/fakeapi_test.go` is an operator's own,
and `liken/kubernetes/fakeapi` serves the tests of `liken`'s
operators. Neither is a model of the API server; a test that needs a
selector or a version check scripts its handler.

Test the operator's handlers through the real reflector, and leave the
reflector's own faults to upstream. The `operators` skill holds the
streaming list a fake must answer and the scenarios a watch must pass.
A CRD schema or a CEL rule is not tested against a fake: the API
server's order of pruning and validation decides the result, so drill
it (see the last section).

## Fake a device

Fake a device at the narrowest interface the code calls, and keep the
protocol real above it.

- **CEC.** `equipment-operator/cec/cectest` implements `cec.Handle`,
  the kernel's CEC ioctls, on a `cectest.Bus` in memory. It answers
  the way the kernel's CEC core does for the calls the repository
  makes, and scripted peers such as a TV answer on the bus. So a real
  `cec.Device` sends and reads real frames.
- **BlueZ.** Build the D-Bus answers that BlueZ returns, such as
  `GetManagedObjects`, with the builders in
  `bluetooth-operator/bluez_test.go`.
- **PipeWire.** Save `pw-dump` output as fixtures in
  `audio-operator/testdata/`. To test the process itself, put a script
  named `pw-dump` first on `PATH` that prints the fixture and exits
  (`stubPWDump` in `audio-operator/pwmonitor_test.go`).
- **Monitors.** Save each real monitor's EDID and modes in
  `display-operator/testdata/`, and parse them in the test.

A test against a real kernel interface, such as `/dev/uinput` in
`bluetooth-operator/uinput_test.go` or the `vivid` CEC adapters in
`equipment-operator/cecnode_vivid_test.go`, skips when the interface
is not open to the user. CI's runner has neither, so the coverage
floor counts only what CI runs. `bluetooth-operator/.testcoverage.yml`
names the tests to skip to measure the same number on a workstation.

## Where a fixture lives

- **Next to its first user.** Put a fixture or a builder directly
  above the first test that uses it, in the same file
  (`cecRoom` in `equipment-operator/cecnode_test.go`).
- **Shared only with a second user.** A helper moves to its own test
  file when a second test file needs it:
  `equipment-operator/assert_test.go` holds the assertions every file
  uses, and `pipenetwork_test.go` the network every fake listens on.
- **Data in `testdata/`.** Real device output, provider responses, and
  golden files go in the package's `testdata/`
  (`liken/init/testdata/grubenv-editenv.golden`). The Go tools skip
  the directory when they look for packages.
- **Small, focused files.** Split a test file by behavior when it
  grows, the way `equipment-operator` splits `cecnode_*_test.go` into
  power, standby, wake, and the rest.

## The coverage gate

Each Go component runs `make test-go` as its `go` job in
`package.toml`. The target checks `gofmt`, runs `go mod tidy -diff`,
fails on any package with no test file (`UNTESTED_PACKAGES`), runs
`go vet`, runs the tests once with `-race -covermode=atomic`, and
checks the profile against `.testcoverage.yml` with
`go-test-coverage`. A package with no test file writes nothing to the
profile, so the gate would never count it; `UNTESTED_PACKAGES` closes
that gap. Each floor sits just under what CI measures, and only
rises: a change that gains coverage raises the number in the same
commit. Copy the target and the config from a sibling component such
as `bluetooth-operator/` for a new one.

## What a test never does

- Start a container, pull an image, or call `docker`.
- Start a cluster: no minikube, kind, k3d, or k3s.
- Reach a host outside the process: no metadata provider, registry,
  forge, or device on the network.
- Sleep or poll on the wall clock to let something happen.
- Depend on hardware that CI lacks without skipping when it is absent.

## Where real proof happens

A fake proves the code against what the project knows about a peer.
These prove it against the real thing, by hand, before a change ships:

- **The `lab` fleet.** The QEMU fleet under `liken/dev-cluster/`,
  `node-1` through `node-5`, runs upgrades, fallbacks, and an
  operator's install. The `dev-cluster-drills` skill holds the steps,
  and its `references/schema-drills.md` holds the drill for a CRD
  schema or CEL change.
- **A component's own `lab/`.** `git-csi-driver/lab/` and
  `per-node-csi-driver/lab/` each boot one `liken` machine in QEMU from
  the release channel, and their drills run there.
- **The physical test cluster.** A device operator's change is proved
  on the real hardware named in its own documentation: a TV on the CEC
  bus, a controller on the radio, a monitor on the GPU.
- **Smoke checks.** Each image can name a script under its component's
  `smoke/` in `package.toml`. CI's image job runs it on each image it
  builds and loads (`mpv/smoke/mpv.sh`). To run one on a workstation,
  build the image with `docker buildx bake <target> --load` and pass
  its reference to the script. This is the one place CI runs Docker.

A container or a local cluster is fine as a temporary experiment on a
workstation, for example to measure a binary's memory. Record what it
measured in a plan, and keep its setup out of the repository.
