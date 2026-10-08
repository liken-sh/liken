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
guides. Each kind has its own reference page with every field.

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
would reserve the hardware. A `SkyQualityMeter`, a `Switch`, or a
`Receiver` names at most one of `telescope` and `observatory`. To
install a device, set its parent field.

| Kind | Short name | Parent field | Key reading in `kubectl get` |
|---|---|---|---|
| `Observatory` | `obs` | none | `weather` |
| `Telescope` | `tel` | `observatory` | the server's host and port |
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
| `FlatPanel` | `flat` | `opticalTrain` | `Lit` or `Dark`, brightness |
| `Dome` | none | `observatory` | azimuth, shutter, park |
| `WeatherStation` | `weather` | `observatory` | safety |
| `SkyQualityMeter` | `sqm` | `telescope` or `observatory` | brightness in mag/arcsec² |
| `Switch` | `sw` | `telescope` or `observatory` | the outputs that are on |
| `Receiver` | `rx` | `telescope` or `observatory` | frequency in MHz |
| `Guider` | none | `telescope` | phase, PHD2's state, RMS in arcsec |
| `Reservation` | `rsv` | `telescope` | phase, step, message |

`GPS`, `Dome`, and `Guider` have no short name, because the singular
is already short. `equipment-operator` also has a `Receiver` kind, so
name this one as `rx` or `receivers.observatory.liken.sh`. Each CRD
declares its parent fields as `selectableFields`, so
`kubectl get cam --field-selector spec.opticalTrain=east-imaging` lists
one train's cameras.

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

The other spec fields are the few that activation needs:
`Observatory.spec.location`, the tube's
`aperture` and `focalLength` in millimeters, the camera's `gain` and
`offset`, the filter wheel's `filters`, the guider's `opticalTrain` and `pulses`, and the
reservation's `telescope`, `holder`, `start`, and `end`. [Plan
05](https://github.com/liken-sh/liken/blob/main/observatory-operator/plans/05-the-property-schema.md) will generate typed fields for the
other standard properties. `kubectl explain` prints every field with
its unit.
