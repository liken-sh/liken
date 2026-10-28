# Working on observatory-operator

This directory holds the operator that controls an observatory's
hardware through INDI, and the manifests that run it: the operator's
`package main` at the top, the resources' Go types in `observatory/`,
the generator of their schema in `schema/`, the INDI client in `indi/`, the client of PHD2's event server in
`phd2/`, the map of driver images in `drivers/`, the generator of the QHY
`Firmware` objects in `firmware/`,
the CRDs, the RBAC, and the operator's `Deployment` in `deploy/`, the
example in `examples/`, the `kubectl liken observatory` plugin in
`cli/` with its image's `Dockerfile.cli`, the manual in `docs/`, the
skills that the manual's guides emit in `skills/`, and the plans. The
plugin shares no code with the operator, as the other operators'
plugins do, and a test holds its list of kinds to the operator's.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it. [Root
plan 74](../plans/completed/74-astrophotography.md) records the contract between
this component and `astrophotography-operator`.

The manual at https://liken.sh/observatory/ is the description of
the operator for the people who run it, and `docs/AGENTS.md` covers
writing it. A change to what a person sees, such as a step, a field, a
condition, or a message, changes the manual in the same commit. The
resource pages are generated from the CRDs, and `skills/` is generated
from the guides, so run `make -C docs generate skills` after an edit to
either.

The manual installs the plugin with `liken plugins sync`, which takes
the plugin of a release. To test a change to `cli/`, against the dev
cluster or any other, build the plugin from the checkout with `make
install-plugin`. It writes `kubectl-liken-observatory` into
`$(HOME)/.local/bin`, or into the directory that `BINDIR` names, and
kubectl finds it there when that directory is on `PATH`. The binary
reports the version `dev`, or the one that `VERSION` names, so it
warns that its version differs from the operator's; `--force`
silences the warning.

`make test` runs every check CI runs. `pods_test.go` holds the
operator's pods to the properties that plan 03 measured, and
`placement_test.go` holds the guide camera's pod on its server's node.

## The resources

`observatory/` holds the Go type of each kind, and `deploy/` holds one
CRD for each kind. Four CRDs are written by hand: `OpticalTube`,
`OpticalTrain`, `Guider`, and `Reservation`. `schema/` generates the
other 15, the kinds with procedures, so the parts that several kinds
share have one source:

- `<plural>-crd.yaml.tmpl` is the CRD of one kind, with the fields
  that only that kind has written out, and the kind's own action
  fields in its template `action`.
- `device.yaml.tmpl` holds the fields that every device kind shares,
  `DeviceSpec` and `DeviceStatus`, and `procedures.yaml.tmpl` holds
  the procedure schema: `spec.activation`, `spec.deactivation`,
  `spec.triggers`, and `status.procedures`.
- `fitskeywords.yaml.tmpl` holds `spec.fitsKeywords`, which the
  `Observatory`, the `Telescope`, and the `Camera` include. Its rule
  refuses the keywords that the camera's driver writes, and the
  generator writes their names from `observatory/fitskeywords.go`, the
  one list of them. The `Reservation`'s CRD holds a copy of the field,
  and a test holds the copy equal to the generated one.
- `<plural>-fields.yaml` gives the typed fields of a device kind, one
  entry for each standard property of its INDI interfaces: the field's
  name, its role, the unit of each number, and its description. The
  role decides whether a field is in spec and status, or in status
  alone. `curated.go` describes the format. The generator writes the
  fields into the CRD, and their Go types into
  `observatory/generated_*.go`.

The standard properties of each interface come from `indi/standard/`:
the captures of the bare drivers give the structure, and
`conventions.yaml` gives the properties that drivers share and libindi
does not define, such as `CCD_GAIN`. The generator fails on a standard
property that a fields file does not name, on an entry that names no
standard property, on an entry or a member with no description, and on
a description that does not name its unit.

The descriptions are ours, written in the voice of `brand/voice.md`,
because `kubectl explain` and the manual publish them. The standard
properties page of `indilib/docs` is a cross-check only: `make
crosscheck` fetches it and writes `schema/disagreements.txt`, which
lists each place where the page, the captures, the conventions, the
simulators, and the units of the fields files disagree, and names the
commit of the page. It reaches GitHub, so no test runs it; a test
checks that each line names a property that a source still defines.

`make schema` writes the CRDs and the generated types again, and a
test fails when one of them is stale. So a new field takes an edit to
a template or a fields file, a description with the field's unit, and
`make schema`.

The tests in `observatory/` read the CRDs through the API server's own
validators and hold them to the Go types:

- `drift_test.go` compares each Go type with its CRD, field by field.
  A field on one side only, or a field with another type, fails.
- `enum_test.go` holds each CRD enum equal to the Go constants.
- `validation_test.go` runs resources that a person could write
  through the structural schema and the CEL rules.
- `example_test.go` holds `examples/simulators.yaml` to the CRDs, to
  the Go types, and to itself: every name it gives exists in it.

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

The transcripts in `indi/inditest/testdata/` are the bytes that a real
`indiserver` sent with one simulator behind it, for each of the 14
simulators that a kind runs. Each directory has `baseline.xml`,
`connect.xml`, and `disconnect.xml`, and the focuser and the dome have
more steps. `replay_test.go` serves them to a client that dials
through `WithDialer`, over a `net.Pipe`, so the tests run in a
`testing/synctest` bubble. A client that sends a wrong message gets no
reply. They come from the simulators of the pinned `indi-simulators`
tag, and `capture.SimulatorScript` in `indi/capture/` holds the
message that each file answers.

`indi/inditest` embeds the transcripts, so a test of another component
reads them with no path into this one. Its `Server` is a fake
`indiserver` for a fixed set of simulators: each driver defines what
its baseline transcript defines, adds what its connect transcript
defines when a client connects it, and answers every other change with
the values sent. A test can hold, refuse, or move a property, and set
its state, report a value, or send a message. The package uses the
standard library alone. The operator's fake servers in
`fakeindi_test.go` take their properties, drivers, and connections
from it.

`indi/inditest/testdata/ccd/frame-header.txt` holds the 80-column header cards,
with trailing spaces removed, of a frame that the CCD simulator saved.
`observatory/fitskeywords_test.go` holds the driver's list of keywords
to it, so a capture after a bump that holds a new keyword fails until
the list names it. `make transcripts` does not write the file, because
its capture runs six simulators on one server and reads the saved
file. To capture it again, run `indiserver` from the `indi-simulators`
image with the telescope, focuser, rotator, filter, SQM, and CCD
simulators, connect each device, slew the mount, write `SCOPE_INFO`,
set `UPLOAD_MODE` to `UPLOAD_LOCAL`, write the one `FITS_HEADER`
keyword `OBJECT`, take an exposure of 1 s, and copy the header of the
file that the driver saved.

The captures in `indi/standard/` hold the standard properties of each
INDI interface. Each one is the union of every property that a bare
driver in `indi/standard/bare/` defined: a small C++ driver for one
interface that turns on every capability and defines nothing of its
own. The properties of `INDI::DefaultDevice` and of the connection
plugins are in `device.xml` alone, and every other capture leaves them
out. The bare drivers build against the libindi of the pinned
snapshots, from their own bake file, because `ci/` publishes every
target it writes. Their sources are here and not in `indi/`, because
a file in `indi/` changes the recipe of the 17 INDI images.

After a bump of the INDI images, `make captures` records both again
in Docker: `make standard` builds and captures the bare drivers, and
`make transcripts` runs the simulators. A test fails when a capture
names another INDI version than `indi/package.toml`. Then run `make
schema`, which fails on each new standard property until a fields file
in `schema/` gives it a field and a description, and `make
crosscheck`, whose report shows what changed between the sources.

## The PHD2 client

`phd2/` speaks PHD2's event protocol, JSON-RPC 2.0 over TCP, one line
of JSON for each message. The reference is the PHD2 wiki page
<https://github.com/OpenPHDGuiding/phd2/wiki/EventMonitoring>, and
where it is silent, `src/event_server.cpp` in `OpenPHDGuiding/phd2`.
PHD2 sends `AppState` only to a new connection, so the client sets the
state from the events that imply one, and reads it again after a stop.
`phd2/phd2test` is a fake event server on `net.Pipe`, which the
package's tests and the operator's tests share. It answers the methods
that guide, `set_exposure`, `loop`, `find_star`, `guide`, and
`dither`, with the events and the error messages of
`src/event_server.cpp` and `src/phdcontrol.cpp`, and holds or fails a
settle when a test asks.

## A second user of the packages

`astrophotography-operator` imports `indi`, `phd2`, and `observatory`,
and its tests run against `indi/inditest` and `phd2/phd2test`. Its
[plan 08](../astrophotography-operator/plans/completed/08-the-component-and-the-first-session.md)
lists this component in that operator's `package.toml`, so CI runs
both components' tests on a change to these packages. Keep them on the
standard library and `kubernetes/conditions`, so the import links no
more of this operator than they are, and run that component's tests
before you change one of their exported names or behaviors.

## The operator

The operator's files are flat in `package main`, one domain to a file.
`operator.go` starts the five kinds of goroutine: the supervisor
(with the finalizers of `finalizers.go`), one
runner for each `Reservation` (`reservation.go`, `activation.go`,
`configure.go`, `deactivation.go`, `steady.go`, `deletes.go`,
`leaves.go`, `finish.go`, `editspending.go`, the guider's
`guidersteps.go` and `guidersteady.go`, and the gates of
`spec.requires` in `requires.go`, which end at the dawn of `dawn.go`),
the status writer (`status.go`, `statustree.go`, `standing.go`,
`readings.go`, `guiderstatus.go`, and `devicesplit.go` and
`typedstatus.go`, which give each property of an INDI device to one
resource's status, and `setup.go` and `attention.go`, which keep a
person's `SetUp` until it expires and set `AttentionNeeded` from a
`notify` action), the lock relay (`locks.go` and
`domepolicy.go`), and the trigger controller (`triggers.go`), which runs each resource's
`spec.triggers` (plan 13), runs an activation that a device missed
(`lateactivation.go`), and runs a failed one again on the retry
annotation (`retry.go`). The runner's `Activation` and
`Deactivation` steps run the lifecycle procedures (`procsteps.go`):
`procedure.go` runs one trigger's actions, `action.go`, `cooler.go`,
and `jobs.go` carry out one action, `resources.go` resolves a
procedure's references, `runs.go` holds the record of each run, and
`activity.go` the `Active` condition. `served.go` holds the rules of
a device that another resource's driver serves, `interfaces.go` what
the operator does with each INDI interface and how it detects one,
`settings.go` the Configure step's write of each setting in a
spec, and `fitskeywords.go` its write of the declared FITS keywords
that the session recorded. Each of them waits on a bell and reads the watches' stores again. `changed`
rings on every watch event, every INDI event, and every change that a
PHD2 reports, and `structure` on the same events except an INDI
property's update or message, and except a PHD2 change other than its
connection or its equipment. The
status writer, the lock relay, and the waits for a property's value
use `changed`. The
supervisor, the trigger controller, and a Ready telescope's runner
use `structure`, so a mount
that reports its position several times a second does not wake them.
`cost_test.go` holds the cost of a reading, and measures the CPU time
of a minute of readings when `OBSERVATORY_COST` names a profile file.
`metrics.go` holds the Prometheus registry and its listener (plan
22), and `series.go` the series of one namespace, which the status
writer, the runners, and the connections set.

- A reservation's `status.steps` is the runner's record. A step that
  runs again after a restart must read what the cluster and the devices
  report before it changes anything, so a change goes out only when it
  is still needed (`indidevice.go`).
- A timer is a clock and its comment says so: `spec.start`,
  `spec.end`, a step's deadline, the status window, the pause after a
  refused write, the pause before the INDI connection is opened
  again, the dawn that ends a gate's wait, and the expiry of a
  person's `SetUp`.
- `drivers/generated.go` comes from `indi/images/` and
  `indi/package.toml`. `make drivers` writes it again, and a test fails
  when it is stale.
- `deploy/firmware/qhy.yaml` comes from `firmware/85-qhyccd.rules`,
  a copy of `libqhy`'s udev rules from the `indi-qhy` image, and from
  the `indi-qhy` tag in `drivers/generated.go`. It holds one `Firmware`
  for `firmware-operator` for each bare QHY chip that the image's
  `fxload` loads. `make firmware` writes it again, and a test fails
  when it is stale. After a bump of the INDI images, `make
  firmware-rules` copies the rules file from the new image in Docker
  and runs `make firmware`. The base in `deploy/` does not name
  `deploy/firmware/`, because the `Firmware` CRD comes from
  `firmware-operator`.

### Tests of the operator

The tests run the operator in a `testing/synctest` bubble against three
fakes, so a step's deadline of 20 minutes takes no real time:

- `fakeapi_test.go` is an API server on `kubernetes/apiservertest`
  that holds every collection, and plays the kubelet: a pod it creates
  is Ready at once unless a test holds it Pending. It plays the Job
  controller too (`fakejobs_test.go`): a `Job` it creates is Complete
  at once unless a test holds it running or makes it fail.
- `fakeindi_test.go` serves INDI from the transcripts above. Each
  server runs a driver for each device pod that its server pod links
  to and that is Ready, defines what the baseline transcript defines,
  adds what the connect transcript defines, and answers every other
  change with the values sent. A test can hold a property, so its
  driver never answers, or refuse it, so its driver answers Alert.
  `fakelocks_test.go` gives the fake mount and dome the lock policies
  of INDI's base classes, and hands each report that a client relays
  to the driver that snoops it. `fakevariants_test.go` joins the
  transcripts of several simulators into a driver that no simulator
  is, such as a Flip-Flat, which serves a `DustCap` and a `FlatPanel`
  on one device.
- `fakeguider_test.go` runs a fake PHD2 from `phd2/phd2test` in each
  guider pod that is Ready, and starts a new one, with nothing
  connected, for each new pod.

`examples/simulators.yaml` is the inventory of most tests, so a change
to the example is a change to them.
