---
name: describe-your-equipment
description: "Describe a real observatory as observatory.liken.sh resources: the site, each telescope, its optical tubes and trains, and one resource per device with the INDI driver and image that run it. Use when setting up someone's mount, cameras, focuser, filter wheel, dome, or other astronomy equipment, when choosing an INDI driver, or when a resource reports ParentFound False."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/describe-your-equipment/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Describe your equipment

This guide turns a real setup into resources. You write one resource
for the site, one for each telescope, one for each tube and light
path, and one for each device. At the end, `kubectl get astro` lists
your equipment, every resource has found its parent, and each device
is `Idle`, ready for a reservation.

The resources only describe the equipment. The operator starts
nothing for them until a `Reservation` holds their telescope, so you
can write and correct them at any time.

Before you start, list the equipment with its make and model. For
each device, note how it connects to the computer: USB serial, USB
with a vendor driver, or a network address. You need that list to
choose drivers, and [Connect USB equipment](https://liken.sh/observatory/docs/guides/connect-usb-equipment/)
needs it to give each device its hardware.

## The tree

Each resource names its parent in its spec, so every reference points
up the tree:

```
Observatory          the site: its location, a Dome, a WeatherStation
 └─ Telescope        one Mount and one INDI server, an optional Guider
     ├─ OpticalTube  aperture and focal length, with no driver
     └─ OpticalTrain one light path through one OpticalTube
          Camera, FilterWheel, Focuser, Rotator, DustCap, FlatPanel
```

A `Telescope` is one mount and everything it carries. Each telescope
has its own INDI server, and every device of the telescope runs on
that server. This matters because INDI drivers read each other only on
the same server: a camera reads the mount's position and the
focuser's position to record them in each frame.

The devices that belong to the site and to no one telescope, such as
the dome and the weather station, run on the observatory's own INDI
server.

Every resource goes in the namespace `observatory`.

## Name the resources

The operator names each pod and `Service` `<resource-name>-<kind>`,
such as `east-mount` for the `Mount` `east`. Give each device the name
of its telescope, so `kubectl get pods` lists one telescope's pods
together. Add a word only when the telescope has two devices of one
kind, such as the cameras `east-main` and `east-guide`.

A resource name is at most 32 characters, and the API server refuses
a longer one when you apply it. The generated name must fit in the 63
characters of a `Service` name, and the cap leaves room for the
longest kind, `skyqualitymeter`.

## Describe the site

The `Observatory` holds the location. The operator writes it to each
mount and GPS, so the mount computes the sky from the right place.
Latitude and longitude are in degrees, with west and south negative,
and the elevation is in meters:

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
light path through a tube, and the cameras, the filter wheel, the
focuser, and the rotator of that path name the train.

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

Use the effective focal length: with a reducer or a flattener in the
path, give the focal length with it. The operator writes the tube's
values to the camera's driver, so the frames record them, and to PHD2
for the guide tube.

A guide scope is a second `OpticalTube` with its own `OpticalTrain`.
An off-axis guider shares the imaging tube, so its train names the
same tube.

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
| `telescope` or `observatory`, at most one | `SkyQualityMeter`, `Switch`, `Receiver` |

A few kinds take settings that the operator writes during activation:

* A `Camera` takes `gain` and `offset`.
* A `FilterWheel` takes `filters`, the filter in each slot from slot 1,
  such as `[Luminance, Red, Green, Blue, H_Alpha, OIII, SII]`.
* Any device takes `power`, the output of a `Switch` that powers it.
  [Automate the equipment](https://liken.sh/observatory/docs/guides/automate-the-equipment/#power-devices-from-a-switch)
  covers power.

`kubectl explain mount.spec` prints every field of a kind with its
unit, and the [reference](https://liken.sh/observatory/docs/reference/) lists them all.

## Choose the driver

The `driver.name` is the name of the INDI driver program, such as
`indi_eqmod_telescope` or `indi_asi_ccd`. The
[INDI device list](https://indilib.org/devices.html) gives the driver
for each product. The operator runs the driver from an image of the
`indi` build, which it selects from the driver's name:

* INDI's own drivers, such as `indi_lx200generic`,
  `indi_celestron_gps`, and `indi_moonlite`, run from the `indi`
  image.
* The simulators, such as `indi_simulator_ccd`, run from
  `indi-simulators`.
* A third-party driver runs from the image that holds it: `indi-zwo`
  for `indi_asi_ccd`, or `indi-open` for `indi_eqmod_telescope` and the
  other drivers that link no vendor SDK. [Drivers and images](https://liken.sh/observatory/docs/reference/drivers-and-images/)
  lists the images and what each one holds.

The CRD does not check a driver name against INDI. A misspelled name
resolves to the `indi` image, the pod starts, and the driver never
appears on the server. The reservation's `StartDevices` step waits for
it and fails after 10 minutes, and its message names the device. Check
the spelling against the INDI device list.

To run a driver that no image of the `indi` build holds, build your
own image and name it in `driver.image`. The operator uses it as
written. The image must hold `/usr/bin/socat` and the driver on
`PATH`, and it must run as user 1000 with a read-only root filesystem
and a writable `/tmp`.

One INDI server runs each driver once, because INDI names a device
after its driver's model, and a second copy of a driver on the same
server breaks the first. So the devices of one telescope must each
name a different driver, and the devices of the observatory too. The
operator refuses a reservation when two devices on one server name one
driver, and its message names both devices and the driver. Two
telescopes can each run the same driver, because each telescope has
its own server.

## Keep spare equipment on the shelf

A device with no parent field is on the shelf. Its spec can name its
driver and its hardware, but the operator starts nothing for it and
claims no hardware for it. Its phase is `Inventory`. To install it,
set its parent field. You can do that while a reservation runs: the
operator starts the device's driver on the running server and connects
it, and the other devices stay connected.

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
of each train, so you can see the tree as the operator reads it:

```sh
kubectl get telescope east -n observatory -o yaml
```

Each device is `Idle` with the message `Not reserved`. Next, give the
devices their hardware in
[Connect USB equipment](https://liken.sh/observatory/docs/guides/connect-usb-equipment/), and then
[reserve the telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/).
