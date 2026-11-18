---
name: describe-your-equipment
description: "Describe a real observatory as observatory.liken.sh resources: the site, each telescope, its optical tubes and trains, and one resource per device with the INDI driver and image that run it. Use when setting up someone's mount, cameras, focuser, filter wheel, dome, or other astronomy equipment, when choosing an INDI driver, when saving the horizon that a telescope sees from where it stands, or when a resource reports ParentFound False."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/describe-your-equipment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Describe your equipment

At the end of this guide, your equipment is written down as
resources: one for the site, one for each telescope, one for each
optical tube and light path, and one for each device. `kubectl get
astro` lists them, each one reports `ParentFound` `True`, and each
device is `Idle` and ready for a reservation.

The resources only describe the equipment. The operator starts
nothing for them until a `Reservation` of their telescope starts, so
you can write and correct them at any time.

Before you start, list the equipment with its make and model. For
each device, note how it connects to the computer: USB serial, USB
with a vendor driver, or a network address. You need that list to
choose drivers, and [Connect USB equipment](https://liken.sh/observatory/docs/guides/connect-usb-equipment/)
needs it to give each device its hardware.

## The tree

Each resource names its parent in its spec:

```
Observatory          the site: its location, a Dome, a WeatherStation
 └─ Telescope        one Mount and one INDI server, an optional Guider
     ├─ OpticalTube  aperture and focal length, with no driver
     └─ OpticalTrain one light path through one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

A `Telescope` is one mount and everything on it. Each telescope has
its own INDI server, and every device of the telescope runs on that
server, because INDI drivers can read each other's properties only on
the same server. A camera, for example, reads the positions of the
mount and the focuser and records them in each frame.

Devices that belong to the whole site, such as the dome and the
weather station, run on the observatory's own INDI server.

All the resources of one observatory go in one namespace, because
they refer to each other by name. This guide uses the namespace
`observatory`, as [Install](https://liken.sh/observatory/docs/guides/install/) does.

## Name the resources

The operator names each pod and `Service` `<resource-name>-<kind>`,
such as `east-mount` for the `Mount` `east`. Give each device the name
of its telescope, so `kubectl get pods` lists one telescope's pods
together. Add a word only when the telescope has two devices of one
kind, such as the cameras `east-main` and `east-guide`.

The API server refuses a resource name longer than 32 characters. With
the longest kind, `weatherstation`, added, the pod and `Service` name
then still fits in the 63 characters that a `Service` name allows.

## Describe the site

The `Observatory` gives the site's location. The operator writes it to
each mount and GPS device, so the mount computes the sky from the
right place. Latitude and longitude are in degrees, with west and
south negative, and the elevation is in meters:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Observatory
metadata:
  name: backyard
spec:
  location:
    latitude: 35.6
    longitude: -105.9
    elevation: 2100
```

## Describe a telescope and its optics

A `Telescope` names its observatory. An `OpticalTube` gives the
aperture and the focal length, in millimeters. An `OpticalTrain` is one
light path through a tube, and the cameras, filter wheel, focuser, and
rotator on that path name the train in their spec.

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Telescope
metadata:
  name: east
spec:
  observatory: backyard
---
apiVersion: observatory.liken.sh/v1alpha1
kind: OpticalTube
metadata:
  name: east-refractor
spec:
  telescope: east
  aperture: 80
  focalLength: 480
---
apiVersion: observatory.liken.sh/v1alpha1
kind: OpticalTrain
metadata:
  name: east-imaging
spec:
  telescope: east
  opticalTube: east-refractor
```

Give the effective focal length: with a reducer or a flattener in the
path, give the focal length with it. The operator writes the tube's
values to the camera's driver, so the frames record them, and to PHD2
for the guide tube. If you edit a tube or a train during a
reservation, the drivers and PHD2 get the new values at the
telescope's next reservation.

A guide scope is a second `OpticalTube` with its own `OpticalTrain`.
An off-axis guider uses the imaging tube, so its train names the same
tube.

The camera's driver writes `TELESCOP` and `INSTRUME` into each frame
with the names of INDI devices, such as `Telescope Simulator`. To
write the names of your own equipment, declare them in
`spec.fitsKeywords` of the `Telescope` and of each `Camera`:

```yaml
kind: Telescope
metadata:
  name: east
spec:
  observatory: backyard
  fitsKeywords:
  - {name: TELESCOP, value: East refractor}
```

The `Observatory` takes `fitsKeywords` too, such as `OBSERVER`.
[FITS keywords](https://liken.sh/observatory/docs/concepts/fits-keywords/) gives the order in
which the keywords of each level merge, and lists the keywords that the
driver measures.

## Describe each device

A device resource names its parent and its INDI driver:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Mount
metadata:
  name: east
spec:
  telescope: east
  driver:
    name: indi_eqmod_telescope
```

The parent field depends on the kind:

| Parent field | Kinds |
|---|---|
| `observatory` | `Dome`, `WeatherStation` |
| `telescope` | `Mount`, `GPS`, `PolarAligner`, `Guider` |
| `opticalTrain` | `Camera`, `FilterWheel`, `Focuser`, `Rotator`, `DustCap`, `FlatPanel` |
| `telescope` or `observatory`, at most one | `Switch`, `Receiver` |

Each kind has a spec field for each standard INDI property of its
interface that a person sets. The reservation's `Configure` step
writes each field that you set to the driver, once. For example:

* A `Camera` takes `gain`, `offset`, and `captureFormat`.
* A `FilterWheel` takes `filters`, the filter in each slot from slot 1,
  such as `[Luminance, Red, Green, Blue, H_Alpha, OIII, SII]`.
* A `Focuser` takes `maxPosition`, `backlash`, and `reverseMotion`.
* A `Switch` takes `outputLabels`, the name of each output.

A field that you leave out keeps the value that the driver sets. When
the driver lacks a property or a member that you set, the `Configure`
step notes it in its summary and continues. The operator writes the
settings again only when the device's driver restarts, so a client
such as Ekos can change a camera's gain for one frame.
`kubectl explain focuser.spec` prints every field of a kind with its
INDI property and its unit, and the [reference](https://liken.sh/observatory/docs/reference/)
lists them all.

A device that runs a driver also takes `power`, the output of a
`Switch` that powers it.
[Automate the equipment](https://liken.sh/observatory/docs/guides/automate-the-equipment/#power-devices-from-a-switch)
covers power.

## A device that another device serves

Some drivers control more than one kind of device. A QHY camera drives
its filter wheel through the camera's cable, and a Flip-Flat is a dust
cap and a flat panel in one box. INDI puts every interface of one
driver on one device, so one driver serves both resources. Write the
resource that runs the driver as usual, and name it in the other
resource's `spec.device` in place of a driver:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Camera
metadata:
  name: east-main
spec:
  opticalTrain: east-imaging
  driver:
    name: indi_qhy_ccd
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

The resource that runs the driver owns the device: it gets the pod,
the claim, and `power`, and the API server refuses `power` or `claim`
on the served resource. The operator connects the owner first, and
then runs the served resource's activation. At the end, the served
resource's deactivation runs before the owner's, so a Flip-Flat's cap
closes before its light goes off and before the device stops. Deleting
either resource waits until its deactivation finishes.

`spec.device` cannot change, and a resource cannot switch from
`driver` to `device` or back. To move a served resource to another
device, delete it and create it again.

The operator sets the camera's `ACTIVE_FILTER` to the camera itself,
because that is how an INDI camera finds the filter of its own wheel
for each frame's header.

The served resource reports the device that serves it in
`status.indiDevice`, and the INDI server in `status.endpoint`, so a
client can connect to it without following `spec.device`. The
operator sets a served resource to `Error`, and its `Ready` message
gives the reason, when:

* `spec.device` names a resource on another INDI server, because one
  INDI device runs on one server.
* The resource it names already serves another resource of the same
  kind.
* The resource it names does not exist, is of the same kind, has a
  `spec.device` of its own, or runs on no server because it has no
  parent field.

A driver serves a kind only when it defines that kind's properties
after it connects, such as `FILTER_SLOT` for a `FilterWheel`. A QHY
camera with no wheel attached defines none, so the `FilterWheel`
reports `Error` and names the missing property, and the rest of the
telescope starts. The reservation's `Connect` step, or `StartSite` for
the observatory's devices, reports it in its summary too. If the
driver defines the property later, the resource becomes `Connected`,
gets its settings, and runs its activation. The operator ignores the
interfaces that a driver lists in `DRIVER_INTERFACE`, because some
cameras list a filter wheel that they do not have.

## Choose the driver

`driver.name` is the name of the INDI driver program, such as
`indi_eqmod_telescope` or `indi_asi_ccd`. The
[INDI device list](https://indilib.org/devices.html) gives the driver
for each product. The operator runs the driver from an image of the
`indi` build, and picks the image from the driver's name:

* INDI's own drivers, such as `indi_lx200generic`,
  `indi_celestron_gps`, and `indi_moonlite`, run from the `indi`
  image.
* The simulators, such as `indi_simulator_ccd`, run from
  `indi-simulators`.
* A third-party driver runs from the image that contains it: `indi-zwo`
  for `indi_asi_ccd`, or `indi-open` for `indi_eqmod_telescope` and the
  other drivers that link no vendor SDK. [Drivers and images](https://liken.sh/observatory/docs/reference/drivers-and-images/)
  lists the images and the drivers in each one.

The CRD does not check a driver name against INDI. The operator runs a
misspelled name from the `indi` image, the pod starts, and the driver
never appears on the server. The reservation's `StartDevices` step
waits for it and fails after 10 minutes, and its message names the
device. Check the spelling against the INDI device list.

To run a driver that no image of the `indi` build contains, build your
own image and set it in `driver.image`. The operator uses it as
written. The image must have `/usr/bin/socat` and the driver on
`PATH`, and it must run as user 1000 with a read-only root filesystem
and a writable `/tmp`.

An INDI server can run each driver only once. INDI names a device
after its driver's model, so a second copy of a driver on the same
server breaks the first. So the devices of one telescope must each
use a different driver, and so must the devices of the observatory.
The operator refuses a reservation when two devices on one server use
the same driver, and its message names both devices and the driver. A
resource that sets `spec.device` runs no driver of its own, so this
rule does not count it. Two telescopes can each run the same driver,
because each telescope has its own server.

## Keep spare equipment in inventory

A device with no parent field is not installed. Its spec can name its
driver and its hardware, but the operator starts nothing for it and
claims no hardware for it, and its phase is `Inventory`. To install
it, set its parent field. A server that runs for a reservation keeps
the devices it started until the reservation ends. So a device that
you install during a reservation starts at the next one, and a device
that you remove keeps running until the reservation ends. [Editing
devices during a
session](https://liken.sh/observatory/docs/concepts/how-a-reservation-runs/#editing-devices-during-a-session)
gives the rule.

## Save the horizon of each place

Trees, buildings, and roofs hide part of the sky above a telescope. A
`Horizon` records, for one place, the altitude below which the sky is
hidden at each azimuth. Two piers at one site see different
treelines, and a portable telescope sees more from the roof than from
the yard. So save one `Horizon` for each place, and set the
telescope's `spec.horizon` to the one for where it is now.

`astrophotography-operator` reads the horizon of a reservation's
telescope. It does not claim or slew to a target that is below the
horizon, and it releases a target that sets below it before the next
frame. `observatory-operator` checks nothing against the horizon, so
a mount still slews behind the trees when KStars tells it to. Check a
target's altitude before you slew to it from KStars.

The profile is a horizon file in the format of N.I.N.A.'s custom
horizon and of Cartes du Ciel's, so you can paste in a horizon that
you traced with a phone app or made for another program. Each line is
one point: the azimuth from 0 to 360 and then the altitude from -90 to
90, in degrees, separated by spaces, a tab, or a comma. A line that
starts with `#` is a comment, and a blank line is skipped.

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Horizon
metadata:
  name: yard
spec:
  profile: |
    # The house to the north, and a row of trees to the west.
    0 30
    40 30
    50 8
    180 5
    230 12
    260 25
    300 25
    330 30
---
apiVersion: observatory.liken.sh/v1alpha1
kind: Telescope
metadata:
  name: east
spec:
  observatory: backyard
  horizon: yard
```

Between two points, the altitude changes linearly with the azimuth,
and the last point joins the first through 360 degrees. The API
server refuses a profile with fewer than two points, a line that is
not a comment, a blank line, or a pair of numbers, an azimuth outside
0 to 360, or an altitude outside -90 to 90.

To move the telescope from the yard to the roof, save a `Horizon`
named `roof` once, and change the name:

```sh
kubectl patch telescope east -n observatory --type merge -p '{"spec":{"horizon":"roof"}}'
```

The telescope's `HorizonFound` condition is `True` when the `Horizon`
that it names exists. When it is `False`, the message names the
missing horizon. The telescope still runs, and
`astrophotography-operator` treats the horizon as 0° at every azimuth
until the `Horizon` exists. A telescope with no `spec.horizon` has no
horizon and no `HorizonFound` condition.

## Check the result

Apply the resources and list them:

```sh
kubectl apply -n observatory -f equipment.yaml
kubectl get astro -n observatory
```

Each resource reports the condition `ParentFound`. When it is
`False`, the message names the parent that does not exist, which is
usually a typo:

```sh
kubectl get astro -n observatory \
  -o custom-columns='KIND:.kind,NAME:.metadata.name,PARENT:.status.conditions[?(@.type=="ParentFound")].status'
```

The `Telescope`'s status lists its tubes, its trains, and the devices
on each train, so you can see the tree as the operator reads it:

```sh
kubectl get telescope east -n observatory -o yaml
```

Each device is `Idle` with the message `Not reserved`. Next, give the
devices their hardware in
[Connect USB equipment](https://liken.sh/observatory/docs/guides/connect-usb-equipment/), and then
[reserve the telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/).
