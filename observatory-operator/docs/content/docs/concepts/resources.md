---
aliases: [/docs/reference/resources/]
title: The resources
weight: 10
---

# The resources

You describe an observatory with resources in the API group
`observatory.liken.sh/v1alpha1`. Most of them describe the site, the
telescopes, and each piece of equipment. A `Reservation` gives one
holder, the person or program that `spec.holder` names, the use of a
telescope. An `OpticalTrain` describes the light
path to a camera, and a `Guider` sets up PHD2 to guide the telescope.
A `Horizon` records the part of the sky that trees and buildings block
at one place. `astrophotography-operator` does not image a target
below the `Horizon`, but `observatory-operator` does not check a slew
against it. Each kind has its own reference page with every field.

Every kind is namespaced and in the category `astro`, so
`kubectl get astro` lists the whole observatory. A resource refers to
its parent by name, and the operator looks the name up in the
resource's own namespace. So all the resources of one observatory go
in one namespace, and two namespaces can each hold an observatory
with the same names. Every reference points up the tree:

```
Observatory          the site: location, Dome, WeatherStation
 └─ Telescope        one Mount, one INDI server, one Guider
     ├─ OpticalTube  aperture and focal length, no driver
     └─ OpticalTrain one light path to one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

A device resource describes the hardware and its settings. The
operator starts the pods of a telescope and of its observatory only
while a `Reservation` of that telescope is active.

A device's parent field is optional. A device with no parent is not
installed anywhere, but its spec can still describe it in full, with
its driver, image, power, and claim. The operator creates no pod, no
`Service`, and no `ResourceClaimTemplate` for it, because a claim
would reserve the hardware. To install the device, set its parent
field. A `Switch` or a `Receiver` sets at most one of `telescope` and
`observatory`.

Each device kind is one INDI interface, such as `CCD` for a `Camera`
and `FILTER` for a `FilterWheel`. One INDI driver can serve several
interfaces on one device: a camera with a built-in filter wheel, or a
Flip-Flat, which is a dust cap and a flat panel. So a device resource
either runs a driver, set in `spec.driver`, or sets `spec.device` to
the resource whose driver serves it on the same INDI server. The
resource that runs the driver gets the pod, the claim, and the power.

The operator treats a device as serving an interface when its driver
defines the property in the table below after the device connects. A
resource whose device does not define it reports `Error`. The
operator ignores the interface bits of the driver's
`DRIVER_INTERFACE`, because some drivers set a bit only after they
find the hardware, and some set a bit for an interface whose
properties they never define. [Describe your
equipment](/docs/guides/describe-your-equipment/#a-device-that-another-device-serves)
shows how to describe a device that another resource's driver serves.

| Kind | INDI interface | A device serves it when it defines |
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

| Kind | Short name | Parent field | What `kubectl get` shows |
|---|---|---|---|
| `Observatory` | `obs` | none | latitude, longitude, the worst `weather` |
| `Telescope` | `tel` | `observatory` | the reservation, its holder, the server's host and port |
| `OpticalTube` | `ota` | `telescope` | `aperture`, `focalLength` in mm |
| `OpticalTrain` | `train` | `telescope` | its camera |
| `Mount` | `mnt` | `telescope` | RA as `19h17m21s`, Dec as `+12°34′56″`, state |
| `GPS` | none | `telescope` | fix, time |
| `PolarAligner` | `pac` | `telescope` | adjustment state |
| `Camera` | `cam` | `opticalTrain` | temperature, setpoint, cooler `On` or `Off`, exposure state or time left |
| `FilterWheel` | `fw` | `opticalTrain` | slot, filter |
| `Focuser` | `foc` | `opticalTrain` | position |
| `Rotator` | `rot` | `opticalTrain` | angle |
| `DustCap` | `cap` | `opticalTrain` | `Open`, `Closed`, or `Moving` |
| `FlatPanel` | `flat` | `opticalTrain` | `LightOn` or `LightOff`, brightness |
| `Dome` | none | `observatory` | azimuth, shutter, park |
| `WeatherStation` | `weather` | `observatory` | safety |
| `Switch` | `sw` | `telescope` or `observatory` | the outputs that are on |
| `Receiver` | `rx` | `telescope` or `observatory` | frequency in MHz |
| `Guider` | none | `telescope` | phase, PHD2's state, RMS in arcsec |
| `Reservation` | `rsv` | `telescope` | phase, step, message |
| `Horizon` | none | none | its age |

`GPS`, `Dome`, `Guider`, and `Horizon` have no short name, because the
singular is already short. A `Horizon` has no parent. A `Telescope`
refers to the `Horizon` of the place where it is set up in
`spec.horizon`, and several telescopes can refer to the same one.
`equipment-operator` also has a `Receiver` kind, so refer to this one
as `rx` or `receivers.observatory.liken.sh`.

Each CRD lists its parent fields in `selectableFields`, so
`kubectl get cam --field-selector spec.opticalTrain=east-imaging` lists
one train's cameras. A `Reservation` selects on `spec.holder` too, so
`kubectl get rsv --field-selector spec.holder=astrophotography-operator`
lists the reservations that `astrophotography-operator` holds. That
operator watches only those.

Every device kind shares these spec fields:

- `driver.name` is the INDI driver, such as `indi_simulator_ccd`. When
  `driver.image` is not set, the name must match `^indi_[a-z0-9_]+$`,
  and the operator runs the image of the `indi` build that has that
  driver.
- `driver.image` is the image to run for the driver. It must contain
  `/usr/bin/socat` and the driver on `PATH`, and it must run as user
  1000 with a read-only root filesystem and a writable `/tmp`.
- `power` is `{switch, output}`: the `Switch` and the output number
  (counted from 1) that power the device. Activation switches it on
  before the device's pod starts, and deactivation switches it off
  after the pod stops.
- `claim` is a `ResourceClaimSpec` for real hardware. A simulator needs
  none.
- `device` is `{kind, name}`: the resource whose driver serves this
  one. Set it instead of `driver`.

A device resource sets `driver` or `device`, never both, and a
resource that sets `device` cannot set `power` or `claim`. You cannot
change `spec.device`, add it, or remove it after the resource is
created.

Each device kind's spec also has a field for each standard property of
its INDI interface that a person sets, such as a camera's `gain` and
`offset`, a filter wheel's `filters`, and a focuser's `maxPosition`.
The reservation's `Configure` step writes each field that the spec sets
to the driver once. The operator writes them again only when the
device's driver restarts, so a client such as Ekos can change a
camera's gain for one frame. A property that the operator computes
from other resources, such as `ACTIVE_DEVICES` and `GEOGRAPHIC_COORD`,
has no spec field. The spec fields of the other kinds are
`Observatory.spec.location`, the tube's `aperture` and
`focalLength` in millimeters, the guider's `opticalTrain` and
`pulses`, the reservation's `telescope`, `holder`, `start`, and
`end`, the telescope's `horizon`, and the horizon's `profile`.

A device kind's status has a typed field for each standard property of
its INDI interface, and the resource that runs the driver lists every
other property in `status.properties`. [Reading the
status](/docs/reference/reading-the-status/) says where each property
appears. `kubectl explain` prints every field with its unit.
