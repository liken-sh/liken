# Working on observatory-operator

This directory will hold the operator that controls an observatory's
hardware through INDI, and the manifests that run it. So far it holds
the Go module with the INDI client in `indi/`, and the plans.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it. [Root
plan 74](../plans/74-astrophotography.md) records the architecture that
this component and `astrophotography-operator` share, and the
simulator tests that support it.

`make test` runs every check CI runs. The `topology/` manifests are
plan 03's, and no code reads them.

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
more steps. `replay_test.go` serves them from a real listener, so a
client that sends a wrong message gets no reply. `make record` records
them again from the pinned `indi-simulators` image; do it after each
bump of the INDI images.

`integration_test.go` runs a real `indiserver` and the focuser
simulator in Docker, in the topology of plan 03, and restarts the
driver's container and the server's container. It skips itself in
`-short` mode, and when Docker or the image is missing.
`INDI_SIMULATORS_IMAGE` names another image, such as one built on a
workstation. The CI runner has Docker, so CI runs it once the pinned
tag is published.
