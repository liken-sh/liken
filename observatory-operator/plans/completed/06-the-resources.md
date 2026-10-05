# 06, The resources

Proposed on 2026-10-05, and settled the same day. Built on 2026-10-05
as the 20 CRDs in `deploy/` and the Go types in the package
`observatory-operator/observatory`. Its tests run the CRDs through the
API server's own validators, and a temporary local experiment applied
them to a Kubernetes 1.33.4 API server. No controller runs yet. "What was
built" states where the build differs from the design below, and "What
the API server showed" gives the results. Plan 05 is not built: the
specs hold only the hand-written fields that activation needs.

## The problem

`astrophotography-operator`, KStars users, and the screens all depend
on `observatory-operator` through its resources. media-operator and
library-operator meet at `Play`, and equipment-operator models each
`Receiver` and `Television` with fields that every model shares and
fields that one vendor adds. The observatory needs an interface of
the same quality between its layers, or the layers above it will read
INDI properties by name and break when a driver changes.

The resources also have to fit more than one rig. A home observatory
has one mount with an imaging train and a guide scope. A larger
observatory has several mounts under one roof, two imaging trains on
one mount, and instruments that share one tube.

## The names

The resources use the words that astronomers use, amateur and
professional:

- A `Telescope` is a mount with its optics and its instruments. Amateur
  usage calls an optical tube on a mount a telescope, and the
  configuration database of Las Cumbres Observatory (LCO) uses
  `Telescope` for the unit that has an aperture, a slew rate, and
  hour-angle limits.
- A `Mount` is the device that points. INDI and ASCOM both call their
  mount drivers "telescope": a `Mount` is a device with INDI's
  `TELESCOPE_INTERFACE`. The collision exists only in driver code.
- An `OpticalTube` is the optical tube assembly, or OTA: the lens or
  mirror that collects the light. It has no driver.
- An `OpticalTrain` is one light path: a tube, and the camera, filter
  wheel, focuser, and rotator behind it. KStars' Ekos uses the same
  term for the same group of devices.
- An `Observatory` is the building and the site: the roof, the weather,
  and the location. LCO puts an enclosure between a site and its
  telescopes. Here the `Observatory` is the enclosure. A `Site` that
  holds several observatories can come later.

## The tree

```
Observatory          the site server, Dome, WeatherStation, policies
 └─ Telescope        one Mount, one indiserver, one Guider
     ├─ OpticalTube  zero or more; passive
     └─ OpticalTrain zero or more; each names one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

Each resource names its parent by name in its `spec`, so every
reference points up the tree, or to a shared resource above it. A
device has one parent field, so it can be in only one place. A person
moves a camera to another train with a change to one field. Ekos works
the other way: an Ekos train lists its devices, and two trains can name
the same camera.

Two trains can name one tube. An off-axis guider is a guide train
that names the imaging train's tube, and a professional telescope with
instruments at several ports is several trains on one tube.

The operator builds the downward view by watching, and writes it to
each parent's status: a `Telescope` reports its trains, its devices,
and whether its guider is ready.

## Ownership

Only the objects that the operator creates have owner references: the
device pods, their `Service`s, the `indiserver` `Deployment`, and the
guider's pod. Deleting a `Telescope` deletes those, through
Kubernetes' garbage collection.

The resources that a person writes have no owner references. An owner
reference deletes its dependents with the owner, so deleting a
`Telescope` would delete the descriptions of every tube and device on
it. A device whose parent is missing stays in place, and its status
states that the parent is missing. media-operator follows the same
rule: a `Play` names its players in `spec.players`, and only the pods
that media-operator creates have an owner reference to the `Play`.

## One INDI server for each telescope

Each `Telescope` has its own `indiserver`, with its mount and the
devices of its trains. The `Observatory` has one more server, for the
devices that no single telescope owns, such as a weather station.

INDI's snooping sets this boundary. A driver reads another driver's
properties only on the same server, and the base classes name exactly
one device of each kind through the `ACTIVE_DEVICES` property:

| Base class | Snoops |
|---|---|
| `INDI::CCD` | `ACTIVE_TELESCOPE`, `ACTIVE_FOCUSER`, `ACTIVE_FILTER`, `ACTIVE_ROTATOR`, `ACTIVE_SKYQUALITY` |
| `INDI::Telescope` | `ACTIVE_DOME`, `ACTIVE_GPS` |
| `INDI::Dome` | `ACTIVE_TELESCOPE` |

So a mount and its trains must share a server, and nothing in INDI
needs two mounts on one. A server for each telescope also limits a
failure to one telescope: the denial of service in plan 01 stops a
whole server with one packet, and a server restart takes every device
on it offline for 3 seconds. INDI has no permission for each device,
so a client of a shared server can slew every mount on it. KStars
connects one Ekos profile to one host and port, which is one
telescope.

The reconciler of plan 08 writes each camera's `ACTIVE_DEVICES` from
its `OpticalTrain`. The train's membership sets what the camera snoops,
and therefore which mount, focuser, and filter wheel each frame's FITS
header names.

A `Telescope`'s status names the `Service` of its server. That
`Service` is the contract that KStars and `astrophotography-operator`
connect through.

## The device kinds

Each kind has typed fields for the standard properties of its kind,
generated in plan 05. Vendor properties appear in status as the device
defines them: the name, the type, the permission, the limits, the
switch rule, and the label. A person can set one by name. No typed
field depends on one.

The first kinds are the ones with a simulator in the `indi-simulators`
image. The plan 05 generator reads its structure from those simulators,
and every kind can be tested with no hardware.

| Kind | INDI interface | Simulator | Parent |
|---|---|---|---|
| `Mount` | `TELESCOPE` | `indi_simulator_telescope` | `Telescope` |
| `Camera` | `CCD` | `indi_simulator_ccd`, `indi_simulator_guide` | `OpticalTrain` |
| `FilterWheel` | `FILTER` | `indi_simulator_wheel` | `OpticalTrain` |
| `Focuser` | `FOCUSER` | `indi_simulator_focus` | `OpticalTrain` |
| `Rotator` | `ROTATOR` | `indi_simulator_rotator` | `OpticalTrain` |
| `DustCap` | `DUSTCAP` | `indi_simulator_dustcover` | `OpticalTrain` |
| `FlatPanel` | `LIGHTBOX` | `indi_simulator_lightpanel` | `OpticalTrain` |
| `PolarAligner` | `PAC` | `indi_simulator_pac` | `Telescope` |
| `GPS` | `GPS` | `indi_simulator_gps` | `Telescope` |
| `Dome` | `DOME` | `indi_simulator_dome` | `Observatory` |
| `WeatherStation` | `WEATHER` | `indi_simulator_weather` | `Observatory` |
| `SkyQualityMeter` | `AUX` | `indi_simulator_sqm` | `Telescope` or `Observatory` |
| `Switch` | `INPUT`, `OUTPUT` | `indi_simulator_io` | `Telescope` or `Observatory` |
| `Receiver` | `SPECTROGRAPH` | `indi_simulator_receiver` | `Telescope` or `Observatory` |

Each kind takes the word that the people who use the hardware use
most, even when another API group has a kind of the same name.
`FlatPanel` is the name imagers use: INDI calls the interface
`LIGHTBOX`, and ASCOM combines a cover and a panel in
`CoverCalibrator`. `Switch` is the name that ASCOM and N.I.N.A. use for
relays and inputs. `Receiver` is a radio receiver, such as an RTL-SDR,
and INDI's base class for it is `INDI::Receiver`. equipment-operator and
Flux each have a `Receiver` kind too, in their own API groups.

INDI's other sensor classes are for radio astronomy too.
`INDI::Spectrograph` is a radio spectrometer, with an antenna and low
and high cut frequencies, and it sets the same `SPECTROGRAPH` bit as
`INDI::Receiver`. `INDI::Detector` and `INDI::Correlator` have no
simulator, and no kind. An optical spectrograph, such as a grating in
front of a camera, is a `Camera`.

The kinds are chosen by the hardware, not by the interface bit. Many
drivers set `AUX` with another bit, such as `AUX` and `POWER` on a
Pegasus power box, or `AUX`, `DUSTCAP`, and `LIGHTBOX` on a Flip-Flat.
`AUX` alone names no kind.

Kinds are added when a person's hardware or a simulator needs one.
Three are left out for now:

- A kind for INDI's `POWER_INTERFACE`. INDI has no simulator for it,
  and the first observatory has no power box. ASCOM and N.I.N.A. treat
  a power box's outputs as a `Switch`, so a power box can be a `Switch`
  when one is added.
- The other drivers that set `AUX`: safety monitors, dew heaters,
  guide-pulse adapters, UPS monitors, and the bridges to SkySafari and
  to other weather stations.
- A generic `Device` kind for drivers with no kind of their own.

## Guiding

A `Guider` belongs to a `Telescope`, because guiding corrects where the
mount points. It runs PHD2, as plan 09 describes, and names the
`OpticalTrain` whose camera guides. A guide scope is a train with its
own small tube. An off-axis guider is a train that names the imaging
train's tube. `spec.pulses` states where PHD2 sends its corrections:
to the mount, or to the camera's ST-4 port.

## Inventory and the `Reservation`

A device resource is inventory. It describes the hardware and its
settings, and the operator starts nothing for it. A `Reservation`
gives one holder the use of one `Telescope`, and the operator starts
the telescope's pods only while a `Reservation` for it is active. The
same object serves both modes:

- In mode 1, a person creates a `Reservation` before they open KStars.
  The holder is that person's desktop client. Plan 10's record of which
  client drives the telescope is this field.
- In mode 2, a `Session` of `astrophotography-operator` creates the
  `Reservation` at dusk and deletes it at dawn.

`Session` stays free for `astrophotography-operator`. Imagers call a
night of targets and exposures an imaging session, and a `Session` in
each layer would need a qualifier in every sentence.

`spec.start` and `spec.end` are optional. With no start, the
reservation starts when it is created. With no end, it lasts until it
is deleted. Both are clock times, so a timer for each one is correct.

The machine next to the telescope is a node, and it stays on. If it
were powered off with the telescope, the cluster would show a
`NotReady` node every day. The devices on it are powered off until a
reservation needs them. Activation and deactivation run in order:

```
activate:    wait for each device's USB device to appear as a DRA device
             → start the device pods as their devices appear
             → start the indiserver → the reconciler connects each device
             → cool the camera → unpark the mount
deactivate:  park the mount → warm the camera → close the dust cap
             → stop the pods → status: safe to power off
```

A person who powers the devices by hand is the first step of
activation, and the `Reservation`'s status lists the devices it waits
for. The order of deactivation protects the hardware: a cooled camera
warms slowly before it loses power, and a mount parks while it still
has power. Without a power switch that the operator controls, the
status "safe to power off" is the only signal a person has.

A device's optional `spec.power` names a `Switch` output. When it is
set, activation switches the output on, and deactivation switches it
off after the pods stop. A relay board that switches 12 V is a common
way to power a rig with no power box, and `indi_simulator_io` makes the
whole lifecycle testable on simulators.

The devices are powered off when no reservation is active, so the
operator cannot discover them. The inventory is the resources a person
writes.

## Policies across telescopes

The `Observatory` holds the policies that span telescopes, such as
"the roof does not close until every mount is parked". The operator
enforces nothing that the `Observatory` does not configure. INDI
already has these policies for one mount and one dome, and each
defaults to off:

- The mount's `DOME_POLICY`: `DOME_IGNORED` or `DOME_LOCKS`.
- The dome's `MOUNT_POLICY`: `MOUNT_IGNORED` or `MOUNT_LOCKS`.
- The dome's `DOME_SHUTTER_PARK_POLICY`: `SHUTTER_CLOSE_ON_PARK` and
  `SHUTTER_OPEN_ON_UNPARK`.

With one mount, the reconciler writes these properties, and the
drivers enforce them while the operator is down. With several mounts,
the dome snoops only one, so the operator enforces the policy across
the servers. The fields are open.

## Drivers and images

A device names its driver, and the operator resolves the image:

```yaml
driver: {name: indi_asi_ccd}
driver: {name: indi_thing, image: ghcr.io/example/indi-thing:1.0}
```

With no `image`, the operator looks the driver up in a map that the
`indi` build writes from the lists in `indi/images/`. The lists name
each driver once, and `check-lists.sh` refuses a build that breaks
that rule, so the map has one image for each driver. Each operator
release embeds the map of the `indi` tag it was tested with.

An `image` is used as written. The documentation states the contract
for it:

- `/usr/bin/socat` exists, because the device pod runs
  `socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`.
- The driver is on `PATH`.
- The driver runs as user 1000, with a read-only root filesystem and a
  writable `/tmp`.

An image built `FROM ghcr.io/liken-sh/indi` meets all three, and its
driver links against the same `libindi` as the server.

## An example

An observatory of simulators, with one telescope, an imaging train, and
a guide scope. The tubes' aperture and focal length are in
millimeters.

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Observatory
metadata: {name: lab}
spec:
  location: {latitude: -30.169, longitude: -70.806, elevation: 2207}
---
kind: WeatherStation
metadata: {name: weather}
spec:
  observatory: lab
  driver: {name: indi_simulator_weather}
---
kind: Telescope
metadata: {name: east}
spec:
  observatory: lab
---
kind: Mount
metadata: {name: east-mount}
spec:
  telescope: east
  driver: {name: indi_simulator_telescope}
---
kind: OpticalTube
metadata: {name: east-refractor}
spec:
  telescope: east
  aperture: 80
  focalLength: 480
---
kind: OpticalTube
metadata: {name: east-guidescope}
spec:
  telescope: east
  aperture: 50
  focalLength: 200
---
kind: OpticalTrain
metadata: {name: east-imaging}
spec:
  telescope: east
  opticalTube: east-refractor
---
kind: OpticalTrain
metadata: {name: east-guiding}
spec:
  telescope: east
  opticalTube: east-guidescope
---
kind: Camera
metadata: {name: east-main}
spec:
  opticalTrain: east-imaging
  driver: {name: indi_simulator_ccd}
---
kind: FilterWheel
metadata: {name: east-wheel}
spec:
  opticalTrain: east-imaging
  driver: {name: indi_simulator_wheel}
  filters: [Luminance, Red, Green, Blue, H_Alpha]
---
kind: Focuser
metadata: {name: east-focuser}
spec:
  opticalTrain: east-imaging
  driver: {name: indi_simulator_focus}
---
kind: Camera
metadata: {name: east-guide}
spec:
  opticalTrain: east-guiding
  driver: {name: indi_simulator_guide}
---
kind: Guider
metadata: {name: east}
spec:
  telescope: east
  opticalTrain: east-guiding
  pulses: Mount
---
kind: Reservation
metadata: {name: east-tonight}
spec:
  telescope: east
  holder: desktop
  end: 2026-10-06T05:00:00-04:00
```

The shape of `holder` is open. It names a person's desktop client in
mode 1 and a `Session` in mode 2.

`examples/simulators.yaml` is the full stack of this example: two
telescopes, a device of every kind, and a `Switch` that powers the
imaging train. Every resource there has an `apiVersion`, and the
example above leaves it out after the first resource.

## Open questions

- **Two cameras of one model.** INDI names a device after its model,
  such as `ZWO CCD ASI183MM Pro`. Each device pod has only the USB
  device it claims, so each driver process finds one camera. Two pods
  of one model would still register one name twice on the server. It
  is not known whether the drivers accept a name from outside.
- **One driver for two kinds.** Many QHY cameras drive their filter
  wheel through the camera's cable, so the wheel's properties are on
  the camera's device. A Flip-Flat is a `DustCap` and a `FlatPanel` in
  one driver. A resource needs a way to name the device that serves it,
  with no driver of its own.
- **A driver name that does not exist.** The inventory is written by
  hand, so a wrong driver name appears only at activation unless the
  operator checks each name against the map when the resource is
  applied, and reports it in status.
- **Two reservations for one telescope.** The operator refuses the
  second one, and states why in its status. Whether a reservation with
  a later start can be created while another is active is open.
- **Policies when the operator is down.** With several mounts, an
  operator that is down enforces no policy across the servers.

## How we test it

The CRDs apply on the `dev-cluster`, and validation refuses a resource
that breaks the schema. The example above applies with no error. No
controller runs yet.

## What was built

The CRDs are written by hand, one file for each kind, as the other
operators of the repository write theirs. The Go types copy the shapes
of `metav1.ObjectMeta` and `metav1.Condition`, so the package imports
no Kubernetes module. Tests in the package hold the two together:
each Go type against its CRD field by field, the fields that every
device shares equal in all 14 device CRDs, each enum against its Go
constants, and the example against the CRDs, the Go types, and
itself.

The build settled these points, which the design above left open or
stated otherwise:

- **Typed fields.** Plan 05 was to generate the typed fields. The
  build wrote by hand only the fields that activation needs: the
  driver, the parent, `power`, `claim`, the camera's `gain`, `offset`,
  and `temperature` setpoint, the filter wheel's `filters`, the
  observatory's `location` and `policies`, the tube's `aperture` and
  `focalLength`, the guider's `opticalTrain` and `pulses`, and the
  reservation's `telescope`, `holder`, `start`, and `end`. Plan 05
  stays open.
- **The parent fields.** A device's spec embeds one of four structs,
  by where the kind can be: `TelescopeDevice`, `TrainDevice`,
  `ObservatoryDevice`, or `TelescopeOrObservatoryDevice`. Each holds
  the parent field and embeds `DeviceSpec`, so each CRD has only the
  parent fields its kind admits. A CEL rule makes a `SkyQualityMeter`,
  a `Switch`, or a `Receiver` name exactly one of `telescope` and
  `observatory`.
- **The driver.** `driver.name` must match `^indi_[a-z0-9_]+$` unless
  `driver.image` is set, and holds no path, space, comma, or colon,
  because the pod runs it under `socat`.
- **The claim.** `spec.claim` is a `ResourceClaimSpec`, kept as raw
  JSON with `x-kubernetes-preserve-unknown-fields`. The API server
  validates it when the operator creates the `ResourceClaim`.
- **The holder.** `spec.holder` is a string.
- **The guider's pulses.** `Mount` or `Camera`, in the case that
  Kubernetes uses for enum values.
- **The device status.** Every device kind has `phase`, `conditions`,
  `observedGeneration`, `indiDevice`, `driver`, `image`, `pod`,
  `node`, and `properties`, which lists every INDI property with its
  members' values and limits and no BLOB data. One `readings` block
  holds the typed values of the kind.
- **The reservation's steps.** `status.steps` lists the activation
  steps `Wait`, `StartSite`, `PowerOn`, `StartDevices`, `Connect`,
  `Configure`, and `Prepare`, and then the deactivation steps `Abort`,
  `Secure`, `Disconnect`, `StopDevices`, `PowerOff`, and `StopSite`.
  Each name is unique, so a JSONPath selects one step by name.
  `status.phase` is `Scheduled`, `Activating`, `Ready`,
  `Deactivating`, `Released`, or `Failed`, and the conditions `Ready`
  and `SafeToPowerOff` serve `kubectl wait`. Plan 07 states what each
  step does.
- **A reservation's telescope.** A CEL transition rule refuses a
  change to `spec.telescope`. Moving an active reservation would leave
  the first telescope running.
- **The category and the short names.** Every kind is in the category
  `astro`. The short names are `obs`, `tel`, `ota`, `train`, `mnt`,
  `cam`, `fw`, `foc`, `rot`, `cap`, `flat`, `pac`, `weather`, `sqm`,
  `sw`, `rx`, and `rsv`. `GPS`, `Dome`, and `Guider` have none,
  because the singular is already short.
- **Field selectors.** Each CRD declares its parent fields as
  `selectableFields`, so the operator can list one telescope's or one
  train's devices.
- **The namespace.** `deploy/` creates the namespace `observatory`
  for the operator and the resources.

## What the API server showed

In the local experiment, `kubectl apply -k deploy/` created the
namespace and the 20 CRDs, and every CRD reached `Established`.
`kubectl apply -n observatory -f examples/simulators.yaml` created the
28 resources of the example with no error, and `kubectl get astro`
listed all of them, one table for each kind. Each short name answered
its kind.

The status subresource took a status written by hand, as the operator
will write it. The printer columns then showed the readings: a camera
at `-9.8` with setpoint `-10`, cooler `42`, and exposure `Busy`, and a
mount's RA and Dec. A printer column shows only the first value of a
JSONPath that matches several, so a `Switch`'s outputs that are on are
also listed in `status.readings.on`, which the `On` column prints
whole as `[1,3]`. `kubectl wait --for=condition=Ready` on the
reservation returned when its `Ready` condition became `True`, and
`kubectl describe` listed each step with its state and times.
`--field-selector spec.opticalTrain=east-imaging` listed one train's
camera.

The API server refused each of these with the message shown:

| Resource | Refusal |
|---|---|
| A `Camera` with no `opticalTrain` | `spec.opticalTrain: Required value` |
| A `Camera` with the driver `ccd` and no image | `driver.name must match ^indi_[a-z0-9_]+$ when driver.image is not set` |
| A `Switch` with a telescope and an observatory | `set exactly one parent: spec.telescope or spec.observatory` |
| A `Focuser` on output 0 | `spec.power.output ... should be greater than or equal to 1` |
| A `Reservation` that ends before it starts | `spec.end must be after spec.start` |
| A `Reservation` moved to another telescope | `spec.telescope cannot change; create another Reservation` |
| A `Guider` with `pulses: mount` | `supported values: "Mount", "Camera"` |
| A `Mount` with a `location` | `strict decoding error: unknown field "spec.location"` |
| An `Observatory` at latitude 91 | `spec.location.latitude ... should be less than or equal to 90` |

A `Camera` with the driver `acme-ccd` in its own image passed a dry
run. The same cases, and more, run in `validation_test.go` through the
API server's own validators.

## References

- INDI's interface bits: `DRIVER_INTERFACE` in
  `libs/indidevice/basedevice.h`, and the interface pages under
  <https://docs.indilib.org/interfaces/>
- What each base class snoops, and the dome and mount policies:
  `libs/indibase/indiccd.cpp`, `inditelescope.cpp`, and
  `indidome.cpp` in `indilib/indi`
- LCO's hierarchy of site, enclosure, telescope, instrument, and
  camera: <https://observatorycontrolsystem.github.io/deployment/configdb_setup/>
- Ekos optical trains:
  <https://indilib.org/forum/development/6250-optical-train-for-kstars-4-0-0.html>
  and <https://kstars-docs.kde.org/en/user_manual/ekos.html>
- ASCOM's device types: <https://ascom-standards.org/alpyca/alpacaclasses.html>
- equipment-operator's `Receiver` and `Television` resources, and
  media-operator's `Play`
- [Root plan 74](../../../plans/74-astrophotography.md), "Resources"
