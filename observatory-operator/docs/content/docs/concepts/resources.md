---
aliases: [/docs/reference/resources/]
title: The resources
weight: 10
---

# The resources

You describe an observatory with 20 kinds of resource in the API
group `observatory.liken.sh/v1alpha1`. Most of them describe the
site, the telescopes, and each piece of equipment. A `Reservation`
gives someone the use of a telescope, and an `OpticalTrain` and a
`Guider` describe how light reaches a camera and how the telescope
guides. A `Horizon` saves the part of the sky that trees and buildings
block from one place, and a `Telescope` names the one for where it
stands now. `astrophotography-operator` images no target below it,
and `observatory-operator` checks no slew against it. Each kind has its own reference page with every field.

Every kind is namespaced, and every kind is in the category `astro`,
so `kubectl get astro` lists the whole observatory. Each resource names
its parent by name in its spec, and a name resolves in the resource's
own namespace, so one observatory's resources all go in one namespace.
Two namespaces can each hold an observatory, with the same names, and
the operator runs them apart. Every reference points up the tree:

```
Observatory          the site: location, Dome, WeatherStation
 └─ Telescope        one Mount, one INDI server, one Guider
     ├─ OpticalTube  aperture and focal length; no driver
     └─ OpticalTrain one light path; names one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

A device resource is inventory: it describes the hardware and its
settings, and the operator starts nothing for it. A `Reservation`
gives one holder the use of one `Telescope`, and the operator starts
the telescope's pods only while the reservation is active. [Plan
06](https://github.com/liken-sh/liken/blob/main/observatory-operator/plans/completed/06-the-resources.md) gives the reasons for the
names and the tree.

Each device's parent field is optional. A device with no parent is on
the shelf: its spec describes it in full, with its driver, image,
power, and claim, but it is installed nowhere. The operator creates
no pod, no `Service`, and no `ResourceClaim` for it, because a claim
would reserve the hardware. A `Switch` or a `Receiver` names at most
one of `telescope` and `observatory`. To install a device, set its
parent field.

Each device kind is one INDI interface, such as `CCD` for a `Camera`
and `FILTER` for a `FilterWheel`. One INDI driver can serve several
interfaces on one device: a camera with a built-in filter wheel, or a
Flip-Flat, which is a dust cap and a flat panel. So a device resource
either runs a driver in `spec.driver`, or names in `spec.device` the
resource whose driver serves it, on the same INDI server. The
resource that runs the driver gets the pod, the claim, and the power.
A device serves its interface when its driver defines the
interface's properties after it connects, and a resource whose device
does not reports `Error`. The interface bit in the driver's
`DRIVER_INTERFACE` does not decide, because some drivers set a bit late
or claim an interface whose properties they never define. [Describe
your
equipment](/docs/guides/describe-your-equipment/#a-device-that-another-device-serves)
shows how to write one.

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
singular is already short. A `Horizon` has no parent: a `Telescope`
names it in `spec.horizon`, and several telescopes can name one.
`equipment-operator` also has a `Receiver` kind, so name this one as
`rx` or `receivers.observatory.liken.sh`. Each CRD
declares its parent fields as `selectableFields`, so
`kubectl get cam --field-selector spec.opticalTrain=east-imaging` lists
one train's cameras. A `Reservation` selects on `spec.holder` too, so
`kubectl get rsv --field-selector spec.holder=astrophotography-operator`
lists the reservations that `astrophotography-operator` holds, and that
operator watches only those.

Every device kind shares these spec fields:

- `driver.name` is the INDI driver, such as `indi_simulator_ccd`. With
  no `driver.image`, the name must match `^indi_[a-z0-9_]+$`, and the
  operator runs the image that the `indi` build lists for it.
- `driver.image` is an image used as written. It must hold
  `/usr/bin/socat` and the driver on `PATH`, and run as user 1000 with
  a read-only root filesystem and a writable `/tmp`.
- `power` is `{switch, output}`: the `Switch` output that powers the
  device, from output 1. Activation switches it on before the device's
  pod starts, and deactivation switches it off after the pod stops.
- `claim` is a `ResourceClaimSpec` for real hardware. A simulator needs
  none.
- `device` is `{kind, name}`: the resource whose driver serves this
  one, in place of `driver`.

A device resource names `driver` or `device`, never both, and a
served resource takes no `power` and no `claim`. `spec.device` cannot
change, and a resource cannot gain it or lose it after it is created.

Each device kind's spec also has a field for each standard property of
its INDI interface that a person sets, such as a camera's `gain` and
`offset`, a filter wheel's `filters`, and a focuser's `maxPosition`.
The reservation's `Configure` step writes each one that the spec
states, once. The operator writes them again only when the device's
driver restarts, so a client such as Ekos can change a camera's gain
for one frame. The settings that the tree decides, such as `ACTIVE_DEVICES`
and `GEOGRAPHIC_COORD`, are never spec fields. The other kinds' spec
fields are `Observatory.spec.location`, the tube's `aperture` and
`focalLength` in millimeters, the guider's `opticalTrain` and
`pulses`, the reservation's `telescope`, `holder`, `start`, and
`end`, the telescope's `horizon`, and the horizon's `profile`.

A device kind's status has a typed field for each standard property of
its INDI interface, and the resource that runs the driver lists every
other property in `status.properties`. [Reading the
status](/docs/reference/reading-the-status/) states which object holds
each property. `kubectl explain` prints every field with its unit.
