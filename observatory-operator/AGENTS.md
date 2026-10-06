# Working on observatory-operator

This directory holds the operator that controls an observatory's
hardware through INDI, and the manifests that run it: the operator's
`package main` at the top, the resources' Go types in `observatory/`,
the INDI client in `indi/`, the map of driver images in `drivers/`,
the CRDs, the RBAC, and the operator's `Deployment` in `deploy/`, the
example in `examples/`, and the plans.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it. [Root
plan 74](../plans/74-astrophotography.md) records the architecture that
this component and `astrophotography-operator` share, and the
simulator tests that support it.

`make test` runs every check CI runs. `pods_test.go` holds the
operator's pods to the properties that plan 03 measured.

## The resources

The CRDs in `deploy/` are written by hand, one file for each kind, and
`observatory/` holds the Go type of each kind. The tests in
`observatory/` read the CRDs through the API server's own validators
and hold them to the Go types:

- `drift_test.go` compares each Go type with its CRD, field by field.
  A field on one side only, or a field with another type, fails.
- `shared_test.go` holds the fields that every device shares equal in
  all 14 device CRDs, descriptions and rules included. A change to
  `DeviceSpec` or `DeviceStatus` is a change to all 14 files.
- `enum_test.go` holds each CRD enum equal to the Go constants.
- `validation_test.go` runs resources that a person could write
  through the structural schema and the CEL rules.
- `example_test.go` holds `examples/simulators.yaml` to the CRDs, to
  the Go types, and to itself: every name it gives exists in it.

So a new field takes three edits: the Go type, the CRD, and a
description with the field's unit. `kubectl explain` prints the
description, and a test fails on a field with none.

## The INDI client

`indi/` speaks INDI 1.7 over TCP with `encoding/xml`. The protocol
reference is <https://docs.indilib.org/protocol/>. Where the reference
is silent, the client follows `libindi`, and a comment names the source
file: `indiserver/ClInfo.cpp` for what the server sends a client, and
`libs/indiabstractclient/` for what libindi's own client does.

These behaviors come from the server or the simulators, and a test
holds each one:

- `getProperties` goes before any `enableBLOB`. `indiserver` reads an
  `enableBLOB` that names a device, before any `getProperties`, as a
  request for that device alone.
- A `delProperty` with no `name` deletes the device. One with an empty
  `name` deletes nothing; the receiver simulator sends it on
  disconnect.
- A device can define a property twice, and can update a property it
  has not defined yet. The store replaces the first definition in
  place, and ignores the update.
- `Settle` reads the answer to a change from the updates after it,
  because INDI has no request identifier. An update counts when it is
  not Busy, and either a Busy came first or it carries the values sent.
  Without the second condition, the position that the dome simulator
  sends on each poll would end the wait before the dome turns.

### Tests

The transcripts in `indi/testdata/` are the bytes that a real
`indiserver` sent with one simulator behind it, for each of the 15
simulators that plan 06 gives a kind. Each directory has `baseline.xml`,
`connect.xml`, and `disconnect.xml`, and the focuser and the dome have
more steps. `replay_test.go` serves them to a client that dials
through `WithDialer`, over a `net.Pipe`, so the tests run in a
`testing/synctest` bubble. A client that sends a wrong message gets no
reply. They were captured
from the simulators of the pinned `indi-simulators` tag, and need
capturing again after a bump of the INDI images.

## The operator

The operator's files are flat in `package main`, one domain to a file.
`operator.go` starts the three kinds of goroutine: the supervisor, one
runner for each `Reservation` (`reservation.go`, `activation.go`,
`configure.go`, `deactivation.go`, `steady.go`, `finish.go`), and the
status writer (`status.go`, `statustree.go`, `readings.go`). Each of
them waits on one bell that every watch event and every INDI event
rings, and reads the watches' stores again.

- A reservation's `status.steps` is the runner's record. A step that
  runs again after a restart must read what the cluster and the devices
  report before it changes anything, so a change goes out only when it
  is still needed (`indidevice.go`).
- A timer is a clock and its comment says so: `spec.start`,
  `spec.end`, a step's deadline, the status window, the pause after a
  refused write, and the pause before the INDI connection is opened
  again.
- `drivers/generated.go` comes from `indi/images/` and
  `indi/package.toml`. `make drivers` writes it again, and a test fails
  when it is stale.

### Tests of the operator

The tests run the operator in a `testing/synctest` bubble against two
fakes, so a step's deadline of 20 minutes takes no real time:

- `fakeapi_test.go` is an API server on `kubernetes/apiservertest`
  that holds every collection, and plays the kubelet: a pod it creates
  is Ready at once unless a test holds it Pending.
- `fakeindi_test.go` serves INDI from the transcripts above. Each
  server runs a driver for each device pod that its server pod links
  to and that is Ready, defines what the baseline transcript defines,
  adds what the connect transcript defines, and answers every other
  change with the values sent. A test can hold a property, so its
  driver never answers, or refuse it, so its driver answers Alert.

`examples/simulators.yaml` is the inventory of most tests, so a change
to the example is a change to them.
