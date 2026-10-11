# 16, Kinds are INDI interfaces

Proposed on 2026-10-11. Built on 2026-10-11, all six steps, and
tested against the fakes and the transcripts. Drilled on liken-1 on
2026-10-11, with INDI simulators; "The drill" below records the
results. This plan replaces
[plan 05](../rejected/05-the-property-schema.md) and the open problem "A
reading rewrites the whole property list", whose measurements it
carries. It breaks the `observatory.liken.sh` API with no conversion,
because no observatory runs on it outside the test clusters.

Each device kind becomes one INDI driver interface, and one INDI device
can serve several resources. Each kind's typed fields come from the
standard properties that libindi defines for its interface, captured
from a bare driver at development time. A device's status keeps every
other property in full, so a person still sees each vendor property
with `kubectl`.

## The problem

Three problems have one cause: the kinds follow the hardware, and INDI
follows interfaces.

- **One driver cannot serve two kinds.** A QHY camera drives its filter
  wheel through the camera's cable, so the wheel's properties are on
  the camera's INDI device. A Flip-Flat is a dust cap and a flat panel
  in one driver. `shareddrivers.go` refuses two resources on one
  driver, so a person cannot describe either one. Plan 06 listed this
  as unsolved.
- **The resources have no typed fields for most standard properties.**
  Plan 06 wrote by hand the few that activation needs. Plan 05 was to
  generate the rest from the simulators, but each simulator adds its
  own properties, such as `SIM_FOCUSING` and `PLANET_MODE`, so its
  output needed a person to sort the standard properties from the
  others.
- **Each status write carries every definition again.** A device's
  `status.properties` holds every property the driver defines, with
  its label, group, permission, rule, ranges, and value. On liken-1 on
  2026-10-11, a reserved simulated `Camera` was 25.1 KB, and 22.3 KB
  of it was 71 properties. Their values were about 2.9 KB. When one
  value changes, the operator writes the whole status, and every
  watcher decodes the whole object again. On the two-node test cluster
  on 2026-10-06, two Ready telescopes made five objects write about
  4.2 times a second, and the operator used 41 to 42m of CPU, against
  0 to 1m with no reservation.

The 45 places where the operator branches on kind show the same
mismatch. 26 of them are about an interface's meaning, such as park,
cool, abort, and the locks. They are written against a kind, so a
device that serves two interfaces fits none of them.

## What INDI defines

These facts come from the source of indi 2.2.5 and indi-3rdparty 2.2.0,
the versions `indi/package.toml` pins. The research notes cite each
file and line.

- **A property is standard by its name.** No `def*Vector` marks a
  property as standard. `libs/indidevice/indistandardproperty.h`
  declares only the six connection properties. The others are the
  names that libindi's base classes and interface mixins define, and
  the names that drivers share by convention and the documentation
  lists, such as `CCD_GAIN`, `CCD_OFFSET`, and `CCD_COOLER`, which no
  file in libindi's `libs/` defines.
- **An interface is a base class, a mixin, or both.** `TELESCOPE`,
  `CCD`, `DOME`, and the sensor family have only a base class. `FOCUSER`,
  `FILTER`, `ROTATOR`, `GPS`, and `WEATHER` have a base class and a
  mixin, and the base class adds properties that the mixin does not,
  such as the focuser's `Presets` and `Goto`. A camera's filter wheel,
  or a power box's focuser, is the mixin alone.
- **One driver publishes all of its interfaces under one device
  name.** No camera driver puts its wheel or its guide head on a
  separate device.
- **The interface bits are late and not always true.** QHY and Atik
  add `FILTER` only at connect, when the camera reports a wheel
  (`indi-qhy/qhy_ccd.cpp:930`). MI, QSI, Apogee, and SBIG claim
  `FILTER` with no wheel. No driver clears a bit on disconnect. Some
  drivers define an interface's properties and never set its bit.
- **A device can snoop itself.** `INDI::CCD` writes the `FILTER` FITS
  keyword only from the slot it snoops from the device that
  `ACTIVE_FILTER` names (`libs/indibase/indiccd.cpp:802`, `:2115`).
  The CCD simulator names itself by default (`:467`), and `indiserver`
  routes a snooped property to every driver that asked for it, the
  sender included (`indiserver/DvrInfo.cpp:230`). So a camera with a
  built-in wheel gets a filter name in its headers only when
  `ACTIVE_FILTER` names the camera.
- **Ten property names have more than one definer.** `ACTIVE_DEVICES`,
  `GEOGRAPHIC_COORD`, `TIME_UTC`, `Presets`, `Goto`, `FITS_HEADER`,
  `UPLOAD_MODE`, `UPLOAD_SETTINGS`, `EQUATORIAL_EOD_COORD`, and
  `ORIENTATION`. No driver in either tree combines two definers of
  one name, so the union of a device's standard sets by name is safe
  at these versions.

## The design

### Each kind is one interface

| Kind | Interface | How the operator detects it |
|---|---|---|
| `Mount` | `TELESCOPE` | `EQUATORIAL_EOD_COORD` |
| `Camera` | `CCD` | `CCD_EXPOSURE` |
| `FilterWheel` | `FILTER` | `FILTER_SLOT` |
| `Focuser` | `FOCUSER` | `FOCUS_MOTION` |
| `Rotator` | `ROTATOR` | `ABS_ROTATOR_ANGLE` |
| `DustCap` | `DUSTCAP` | `CAP_PARK` |
| `FlatPanel` | `LIGHTBOX` | `FLAT_LIGHT_CONTROL` |
| `PolarAligner` | `PAC` | `PAC_MANUAL_ADJUSTMENT` |
| `GPS` | `GPS` | `GPS_REFRESH` |
| `Dome` | `DOME` | `DOME_MOTION` or `DOME_SHUTTER` |
| `WeatherStation` | `WEATHER` | `WEATHER_STATUS` |
| `Switch` | `OUTPUT` and `INPUT` | `DIGITAL_OUTPUT_1` or `DIGITAL_INPUT_1` |
| `Receiver` | `SPECTROGRAPH` | `RECEIVER_SETTINGS` |

Each property in the detection column is in the spike's captures or
in libindi's source. Step 1 confirms each one against the capture of
its interface.

The operator decides that a device serves an interface from the
properties the device defines after connect, and reads the bit in
`DRIVER_INFO.DRIVER_INTERFACE` only as a hint. This rule has no
exceptions: the same check finds a QHY camera's wheel, refuses an SBIG
camera that claims a wheel it does not have, and tells `Receiver` from
`INDI::Spectrograph`, which set the same bit.

`Switch` is the one kind with two interfaces, because a relay board
has outputs and inputs on one device. Its typed fields are the union
of the two mixins' captures.

`SkyQualityMeter` goes. Its simulator sets no interface bit, and
`SKY_QUALITY` is not a property libindi defines. A sky quality meter
can return as a kind when someone's hardware needs one.

`GUIDER` has no kind. It is the ST-4 pulse mixin on a camera or a
mount, and the `Guider` (PHD2) already names where its pulses go with
`spec.pulses`.

### One device serves several resources

A device resource either runs a driver or names the resource whose
device serves it:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Camera
metadata:
  name: east-main
spec:
  opticalTrain: east-imaging
  driver: {name: indi_qhy_ccd}
---
apiVersion: observatory.liken.sh/v1alpha1
kind: FilterWheel
metadata:
  name: east
spec:
  opticalTrain: east-imaging
  device: {kind: Camera, name: east-main}
  filters: [L, R, G, B, Ha, OIII, SII]
```

A CEL rule requires exactly one of `driver` and `device`. The resource
that runs the driver is the device's owner. It gets the pod, the
claim, `spec.power`, and the finalizer, and its pod's lifecycle covers
every resource it serves:

- The owner gets the finalizer when the operator creates its pod, as
  today. A served resource gets the finalizer when its owner does, so
  a delete of either waits for the deactivation.
- A served resource's activation runs after its owner connects, and
  its deactivation runs before its owner's. A Flip-Flat's `DustCap`
  closes before the device stops.
- A served resource that leaves its server, because it is deleted or
  because its owner's pod stops, runs its deactivation first, as a
  device that leaves does today.
- A served resource names a resource on the same server. A pointer
  across servers is refused with a condition, because one INDI device
  runs on one server.
- When the owner connects and does not define the interface, the
  served resource reports `Error` and names the missing property. A
  QHY camera with no wheel attached fails here, at activation, and the
  rest of the telescope still starts.

`shareddrivers.go` keeps its rule for two resources that each run the
same driver. A served resource runs no driver, so that rule does not
see it. Two served resources of one kind on one device are refused.

Every device's status reports the INDI device name and the server
endpoint it resolved to, the served ones included. A client such as
`astrophotography-operator` reads one resource and reaches the device
without following the pointer.

### What the operator writes to INDI

The rules of today stay, with the served device in place of the
resource's own:

- A camera's `ACTIVE_DEVICES` names the INDI device of each device in
  its train. When the train's `FilterWheel` is served by the camera,
  `ACTIVE_FILTER` names the camera. That is INDI's own arrangement, so
  the operator has no case for it.
- Typed spec fields are written in the `Configure` step, once for each
  reservation, as today. The operator does not drive them back while
  the reservation runs, so a sequencer can change a camera's gain for
  one frame.
- Settings that the tree decides, such as `ACTIVE_DEVICES`,
  `GEOGRAPHIC_COORD`, `SCOPE_INFO`, and `DOME_POLICY`, never become
  spec fields, so a person and the operator never write the same
  property.

### The standard set of each interface

A bare driver is a small C++ driver for one interface. It hosts the
interface's mixin on a plain `INDI::DefaultDevice`, or inherits the base
class for an interface that has no mixin, turns on every capability,
defines nothing of its own, and reports connected with no hardware.
What it defines is libindi's standard set for that interface. A spike
on 2026-10-11 built six of them against libindi 2.2.5, captured them
under `indiserver` in 38 s, and found:

| Interface | Before connect | Added at connect |
|---|---|---|
| `CCD` | 7 | 49 |
| `TELESCOPE` | 8 | 26 |
| `FILTER` | 6 | 2 |
| `FOCUSER` | 6 | 14 |
| `DOME` | 8 | 21 |
| `DUSTCAP` and `LIGHTBOX` | 7 | 4 |

The spike used the focuser's base class, and step 1 moves it to the
mixin. Three interfaces cannot turn on every capability at once:
`FOCUSER` loses `FOCUS_TIMER`, a `TELESCOPE`'s park position members
follow its park type, and a `DOME` is a roll-off roof or a rotating
dome. Each of those needs one capture for each variant, and the
generator merges them. Some properties appear only after a value
changes, such as `TELESCOPE_PIER_SIDE`, so a capture keeps every
definition it sees, not only the last state.

The standard set of an interface is the merge of two sources:

1. The bare driver's capture: the structure, the types, the
   permissions, and the rules.
2. A short table, written by hand, of the properties that drivers
   share by convention and libindi does not define, such as
   `CCD_GAIN`, `CCD_OFFSET`, `CCD_COOLER`, `CCD_COOLER_POWER`, and
   `FOCUS_TEMPERATURE`. The simulator transcripts check each entry.

The capture leaves out the properties of the transport and of
`DefaultDevice`, such as `CONNECTION_MODE`, `DEVICE_PORT`, `DEBUG`,
and `POLLING_PERIOD`, because they belong to every device and to no
interface.

A curated file for each kind, in `observatory-operator/schema/`,
names each typed field, gives its role, states its unit, and
describes it. The descriptions are ours, in the project's voice,
because `kubectl explain` and the manual publish them. The generator
fails on a standard property that has no entry, so no standard
property is left untyped by accident, and on an entry with no
description. Where the driver chooses the elements, such as slew
rates, track modes, capture formats, park positions, and filter
names, the typed field is a list of the elements the driver defines.

The standard properties page of `indilib/docs` is a cross-check at
development time, and the generator never reads it, because its text
is not ours to publish. A make target fetches the page and writes a
report of each place where the page, the captures, the table, the
simulators, and the curated units disagree, with the commit of the
page. The report is committed, and picks no side.

The generator writes each CRD with procedures whole, from a template
for each kind and the shared templates of the device fields and the
procedure schema, so those shared parts have one source.

The bare drivers run on a workstation, never in CI and never in a
cluster. Their sources are in `observatory-operator/indi/standard/bare/`,
not in `indi/`, because every file in `indi/` is part of the recipe of
the 17 INDI images, and a new one would rebuild them all. A make target
builds the drivers on the pinned snapshots, from a bake file of their
own, runs each one under `indiserver`, and captures it.
The captures are committed under `observatory-operator/indi/standard/`,
and a test fails when a capture names another INDI version than
`indi/package.toml`.

### The status

Each property appears in exactly one object, so one update costs one
write:

- A standard property is a typed field on the resource of its
  interface. A `FilterWheel` served by a camera shows `FILTER_SLOT` and
  `FILTER_NAME`, and the camera does not.
- Every other property is in `status.properties` of the owner, in full,
  with its definition and its value, as today. That includes vendor
  properties and the properties of `DefaultDevice`, such as `DEBUG`.
- The schema states the definition of a typed field, so its status
  holds the value alone. The ranges that the driver chooses, such as a
  camera's temperature limits, are in `status.limits`.

A simulated `Camera` then writes about 11.8 KB in place of 25.1 KB.
The write rate does not change in the simulator: its camera copies the
mount's right ascension into `EQUATORIAL_PE`, a vendor property, every
second. Nobody has checked whether a real camera driver defines a
property that changes that often.

The schema of each kind grows, because each typed field brings its
description. The procedure schema is 70% of the `Camera` CRD today and
stays as it is; a later plan can return to it.

## Steps

Each step closes in its own commit with its tests.

0. **Check the two unknowns, and take one saving now.**
   - Run the CCD simulator with `ACTIVE_FILTER` naming itself, take a
     frame, and check that its FITS header carries `FILTER`. Keep it as
     a test on the transcripts.
   - Read a reserved `Camera` with `--show-managed-fields` on liken-1. A
     model puts the `managedFields` at about 28 KB, more than the
     status. If they are that large, the way the operator writes status
     is a cost of its own, and this plan takes it up.
   - Drop `status.properties` from the operator's informer cache with
     the `Transform` option of `kubernetes/informer`. The operator never
     reads it from the cache, and today it decodes it on every
     snapshot.

   **Results of step 0**, on 2026-10-11:

   - The CCD simulator of the pinned `indi-simulators` image, with
     `ACTIVE_FILTER` naming itself, wrote `FILTER = 'Green'` in the
     header of a frame taken after it selected slot 2. The steps are
     in `indi/testdata/ccd/` (`frame`, `slot`, `expose`), and
     `TestCameraFrameNamesTheFilterOfItsOwnWheel` reads the header.
   - On liken-1, a reserved simulated `Camera east-main` with 71
     properties measured:

     | Part | Bytes |
     |---|---|
     | The whole object | 53,965 |
     | `metadata.managedFields` | 29,290 |
     | `status` | 23,594 |

     The operator's status entry in `managedFields` was 28,411 bytes,
     because `status.properties` and each property's `members` were
     lists of type `map` keyed by `name`, and the entry names every
     key. The operator writes each status as a `PUT` of the whole
     object to `/status`, with no field manager, so a merge by key
     gains nothing. Both lists are now `atomic` in the 14 device CRDs.
     Applied to the measured objects, the camera's `managedFields`
     become about 1.8 KB and the whole object about 26.5 KB.
   - The operator's watches drop `status.properties` from the cached
     device objects; the informer already drops `managedFields`. The
     status writer compares a composed status with the cached one
     when its memo has no record of the object's version, after a
     restart or a write by another client. For a connected device
     that comparison now fails, and the writer sends one `PUT`. The
     API server finds it unchanged and stores nothing, so the cost is
     one request, with no write to etcd and no watch event. A `PUT`
     of an unchanged status on liken-1 kept its `resourceVersion`.
1. **The bare drivers and the capture.** The sources in `indi/standard/bare/`,
   the make target, and a capture command in Go that also captures the
   simulator transcripts in `indi/testdata/` again, so both are made the
   same way after an INDI bump. The captures for every interface in the
   table, with the variants above.
2. **The generator.** The curated files, the hand-written table, the
   templates, and the cross-check with the documentation. It writes
   the Go types and the 16 CRDs with procedures whole, with the shared
   device and procedure schema that `shared_test.go` and
   `procedureschema_test.go` held equal by hand.
3. **The kinds as interfaces.** Detection from properties, `spec.device`
   and its CEL rule, the owner's lifecycle over its served resources,
   the resolved device and endpoint in every status, and the branches
   on kind moved to the interface. `SkyQualityMeter` goes from the
   types, the CRDs, the example, and the manual.
4. **The status.** Typed fields from the generated types, `status.limits`,
   and `status.properties` for the rest. The readings and the display
   columns read the typed fields.

   **Results of step 4**, on 2026-10-11:

   - The build splits each INDI device's properties among the resources
     it serves (`devicesplit.go`). A name in two standard sets, such as
     `GEOGRAPHIC_COORD` on a mount that serves a GPS, goes to the served
     resources first, in the order of the tree, and then to the
     resource that runs the driver. A served resource exists only to
     show its interface, and the resource that runs the driver keeps
     everything else.
   - Each value is in one place, so the readings that restated a typed
     field are gone. Two readings stay, because each derives one value
     from several properties and no typed field holds it: a `Mount`'s
     `readings.state` and a `FilterWheel`'s `readings.filter`. The
     display and the printer columns read the typed fields, and the
     columns of a dome's park and shutter, a dust cap's cover, a flat
     panel's light, and a weather station's safety read the reasons of
     the state conditions. A `Switch` shows its outputs that are on in
     `display.on`. The readings and the state conditions read the
     properties that the resource holds, the same ones that its typed
     fields hold in the same window.
   - `status.limits` maps each INDI property, then each member, to
     `min`, `max`, and `step`, for every number of the typed fields that
     a client can write. A range whose `min` equals its `max` is left
     out, because libindi's clients read it as no range. The map is
     atomic.
   - The informer cache drops the typed fields and `status.limits` as
     well as `status.properties`.
   - On the fakes, a reserved `Camera east-main` writes 12.5 KB in
     place of 23.6 KB: 3.1 KB of typed fields, 1.4 KB of limits, 6.7 KB
     of the simulator's own properties and `DefaultDevice`'s, and 1.3 KB
     of the rest. The `FilterWheel` it serves writes 1.2 KB.
5. **The manual and the example.** The resource pages, the guide to
   describing equipment, and the example: `FilterWheel east` served by
   `Camera east-main`, because the CCD simulator serves `FILTER`.

## Tests

The tests run on the fakes, as today, against the transcripts and the
captures:

- A `FilterWheel` served by `Camera east-main` reaches `Ready` on the
  CCD simulator's transcript, and the camera's `ACTIVE_FILTER` names
  the camera.
- A frame from that camera carries the `FILTER` keyword (step 0).
- A served resource whose owner does not define the interface reports
  `Error` with the missing property, and the telescope still starts.
- A device that sets the `FILTER` bit and defines no `FILTER_SLOT`
  serves no `FilterWheel`.
- A `DustCap` and a `FlatPanel` served by one device both activate and
  deactivate, the cap closes before the device stops, and a delete of
  either one waits for its deactivation.
- A served resource that names a resource on another server is
  refused.
- Each property appears in one object: a `FILTER_SLOT` update writes the
  `FilterWheel`'s status and not the camera's.
- The generated types match the CRDs, and the captures match
  `indi/package.toml`.

## The drill

On liken-1, with the simulators: reserve `east` with `FilterWheel east`
served by `Camera east-main`, and confirm that both reach `Ready`,
that the camera's pod is the only pod for the two, and that a captured
frame's header names its filter. Read both objects' sizes and their
`managedFields`, and the operator's CPU with two telescopes Ready,
against the figures above.

### Results

The drill ran on 2026-10-11 against `2026.10.11-004-dev-009-35065a74`.
The old operator released the `SkyQualityMeter lab` and the
`FilterWheel east` that ran its own driver before the bump, because
Flux prunes the `SkyQualityMeter` CRD and `spec.device` cannot replace
`spec.driver` on an existing object. After the bump, all 19 CRDs were
established, the operator rolled out with no restart, and every device
in `observatory` was `Idle`.

| Check | Result |
|---|---|
| Reserve `east` | `Ready` in 42 s. `FilterWheel east` and `Camera east-main` both reported `Ready`, with `indiDevice` `CCD Simulator` and the endpoint `east-telescope.observatory.svc:7624`. `east-main-camera` was the one pod for the two. |
| The camera's `ACTIVE_FILTER` | `CCD Simulator`, the camera itself. The `FilterWheel` showed `slot` and `filters` as typed fields, and the camera's `status.properties` held 17 properties, with no `FILTER_SLOT` or `FILTER_NAME`. |
| A frame through the wheel | Slot 3 gave `FILTER = 'Green'` in the FITS header, and slot 5 gave `FILTER = 'H_Alpha'`. The `FilterWheel` followed to slot 5 and `H_Alpha`. |
| Reserve `west` as well | `Ready` in 7 s. |
| End both `Reservation`s | `west` released in 19 s and `east` in 1 min 12 s. The deactivation warmed the camera, closed the dust cap, and parked both mounts and the dome. `FilterWheel east` went `Idle` with its camera, and every device pod stopped. |
| `Event`s | One for each transition and action. No `Event` counted more than 2, and a change of slot posted none. |

Each object, compact JSON, read with `--show-managed-fields` while
`east` was `Ready`:

| Object | Whole | `managedFields` | `status` | `status.properties` |
|---|---|---|---|---|
| `Camera east-main`, step 0 | 53,965 | 29,290 | 23,594 | 71 |
| `Camera east-main` | 19,512 | 5,092 | 13,351 | 17 |
| `FilterWheel east` | 3,490 | 1,390 | 1,190 | 0 |
| `Mount east` | 17,989 | 3,268 | 13,599 | 29 |

The camera's status entry in `managedFields` was 4,377 bytes, against
28,411 in step 0. Its status was 13.4 KB, against 12.5 KB on the
fakes.

With both telescopes `Ready` for 5 minutes, the operator used 60 to
63m of CPU and 19 MiB. With no reservation, it used 1 to 2m, and no
device object changed in a minute. Plan 15's drill on liken-1 measured
115m with both telescopes `Ready`. The 41 to 42m in "The problem" came
from the two-node test cluster, so it does not compare directly.

The write rate did not fall. In one minute, the watches saw 270
changes, 54 each on `Mount east`, `Mount west`, and the three
`Camera`s, about 4.5 a second. The mount simulators move their right
ascension about once a second, and each CCD simulator copies it into
its own `EQUATORIAL_PE`, so each tick writes the mount and every
camera. Plan 16 made each of those writes smaller, not less frequent.

The drill found two small things. Neither one affects a reservation:

- An `Idle` device keeps the `status.endpoint` of its last
  reservation, with no pod.
- `east-switch`, `lab-dome`, and `lab-weatherstation` each started
  their container a second time in the second before the operator
  deleted their pods, when the INDI server they connect to stopped
  first.

### Second drill

The second drill ran on 2026-10-11 against
`2026.10.11-004-dev-016-a2a7bc67`. Three fixes landed after the first
drill: bde12a71 clears `status.endpoint` when the driver's pod is
gone, 3e22936e deletes the device pods before their INDI server, and
747d3270 runs the deactivation of a device deleted with its parent.
A run on `2026.10.11-004-dev-015-cd6ed7bf` found that a bulk delete
skipped the deactivation of `Camera east-main` and `FlatPanel east`,
which stayed while their `OpticalTrain` was deleted, and a2a7bc67
fixed that. The operator rolled out with no restart, all 19 CRDs were
established, and the 27 objects in `observatory` were unchanged.

| Check | Result |
|---|---|
| Idle after the bump | Every device `Idle`, and `Focuser spare` `Inventory`. No object had `status.endpoint`. |
| Reserve `east` | `Ready` in 40 s. `FilterWheel east` and `Camera east-main` reported `Ready` with `indiDevice` `CCD Simulator` and the endpoint `east-telescope.observatory.svc:7624`, and `east-main-camera` was the one pod for the two. |
| The camera's `ACTIVE_FILTER` | `CCD Simulator`. The `FilterWheel` showed typed `slot` and `filters`, and the camera's 17 `status.properties` held no `FILTER_SLOT` or `FILTER_NAME`. |
| A frame through the wheel | Slot 3 gave `FILTER = 'Green'`, and slot 7 gave `FILTER = 'SII'`. The `FilterWheel` followed to slot 7. |
| Reserve `west` as well | `Ready` in 6 s. |
| `Event`s | 154 while both reservations ran, each with a count of 1. |
| End both `Reservation`s | `west` released in 19 s and `east` in 1 min 9 s. The deactivation warmed the camera, closed the dust cap, and parked both mounts and the dome. `FilterWheel east` went `Idle`, every device pod stopped, and no object kept `status.endpoint`. |
| Device pods at stop (3e22936e) | A pod watch through both releases saw every `restartCount` stay at 0. |
| Bulk delete (747d3270, a2a7bc67) | One `kubectl delete` of `Reservation east-bulk`, `OpticalTrain east-imaging`, and `DustCap east` while `east` was `Ready`. Before the finalizers were released, the cap closed, `Camera east-main` warmed to 5 °C with its cooler off, `FlatPanel east` ran its deactivation, and the mount and the dome parked. The Disconnect step disconnected `Camera east-main` first. The release took 1 min 8 s, and every pod stopped. |

Each object, compact JSON, read with `--show-managed-fields` while
`east` was `Ready`. The sizes are within 50 bytes of the first drill:

| Object | Whole | `managedFields` | `status` | `status.properties` |
|---|---|---|---|---|
| `Camera east-main` | 19,464 | 5,092 | 13,303 | 17 |
| `FilterWheel east` | 3,486 | 1,390 | 1,186 | 0 |
| `Mount east` | 17,978 | 3,268 | 13,588 | 29 |

With both telescopes `Ready` for 5 minutes, the operator used 58 to
65m of CPU and 18 to 20 MiB, with one sample of 71m 30 s after `west`
became `Ready`. In one minute, the watches saw 270 changes, 54 each
on `Mount east`, `Mount west`, and the three `Camera`s, the same as
the first drill.

The bulk delete showed two smaller faults. The PowerOff step did not
switch off outputs 1, 2, and 3 of `Switch east`, which a normal
release switches off, because `powerOff` finds devices through
`tree.devicesOn`, and that reaches a train's devices only through the
train. As their pods stopped, the six devices of the deleted train
each posted a `Starting` `Event`, though no pod started. Five said
"Starting on east-telescope", and `FilterWheel east` said "Waiting
for activation to create pod east-main-camera".

## Deferred

- **`PowerBox`.** `POWER` shares no property shapes with `OUTPUT`, 14
  drivers use it, and INDI has no simulator for it. `Switch` keeps
  `spec.power` until someone's hardware needs a power box.
- **`INPUT` on its own**, `AO`, `IMU`, `DETECTOR`, and `CORRELATOR`
  have no kind.
- **The procedure schema.**
- **A driver with several INDI devices.** The operator still takes the
  first device whose `DRIVER_EXEC` matches.

## Open questions

None. `ci/` cannot declare a bake target that it never publishes,
because it writes a target only for an image it publishes. So the bare
drivers build from a bake file that only the make target uses.

## References

- indilib/indi at `v2.2.5`: `libs/indibase/`, `libs/indidevice/basedevice.h`
  (`DRIVER_INTERFACE`), `indiserver/DvrInfo.cpp`
- indilib/indi-3rdparty at `v2.2.0`: the survey of drivers with more
  than one interface found 45 of 315 drivers with more than one bit
  other than `AUX`, and 53 when the bits added at connect are counted.
- Standard properties: <https://docs.indilib.org/drivers/standard-properties/>
- [docs#7](https://github.com/indilib/docs/issues/7): the standard
  properties page is maintained by hand.
- Root [plan 74](../../../plans/74-astrophotography.md): what
  `astrophotography-operator` reads from these resources.
