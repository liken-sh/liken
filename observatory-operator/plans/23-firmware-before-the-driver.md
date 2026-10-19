# 23, Firmware before the driver

Proposed on 2026-10-19. Not built. It waits for `liken`'s
[milestone 87](../../liken/plans/87-firmware-a-device-takes-from-the-host.md),
which adds the `Firmware` resource and the upload that machine-operator
runs. This plan ships the `Firmware` objects for QHY cameras and the
files they need in `indi-qhy`. The operator's own code does not
change.

## The problem

Some cameras hold no firmware of their own. A QHY5L-II powers up as a
bare Cypress FX2 chip, and the host must write the camera's firmware
into it before any driver can open it. On a normal Linux host, a udev
rule in INDI's `libqhy` does that: it matches the bare chip's vendor
and product, and runs `fxload` with the firmware file for that model.
The chip then resets and enumerates again as the camera.

A `liken` machine runs no udev rules, so nothing loads the firmware.
The bare chip publishes as a device with no kernel driver, and
`indi_qhy_ccd` cannot open it. The `indi-qhy` image says so, and the
`connect-usb-equipment` guide lists it as equipment that does not work
yet.

INDI's `85-qhyccd.rules` holds 117 rules: 18 for FX2 chips, 32 for
FX2LP, and 67 for FX3. Eleven of them also load a second-stage loader
with `fxload -s`. Of the 117, 105 match QHY's own vendor ID, `1618`.

## What the lab measured

On 2026-10-19 a hand-written `Job` loaded a QHY5L-II-C on the test
cluster, behind a powered USB 3 hub:

- The bare chip published as `1618:0920`, with no serial number and no
  strings, and the `Job`'s claim delivered its node,
  `/dev/bus/usb/001/012`.
- `fxload -t fx2 -I QHY5II.HEX` wrote 5545 bytes in 61 segments and
  reset the chip. The file came from `libqhy/firmware/` in INDI's
  `indi-3rdparty` repository, and the rule for `0920` in that
  directory's `85-qhyccd.rules` names it.
- The chip disconnected 14 seconds after the reset and enumerated 2
  seconds later on the same port as a new device: `1618:0921`, with
  the strings `QHY-CCD` and `QHY5-II`. `liken` published it under the
  same device name, `usb-1-4-1-2`, with the new product.
- The `Job` ran in `debian:trixie-slim` as root, and installed `fxload`
  and `curl` at its start. The whole `Job` took 17 seconds.
- The firmware stays until the camera loses power. A power cycle
  brings the bare chip back.

The camera after the load is a different USB device from the chip
before it: a new product ID on a new device number. A claim on one does
not follow to the other. That makes this a different problem from
[a replugged USB device](open-problems/a-replugged-usb-device.md),
where the same device comes back.

## The design

`liken` owns the mechanism and this component owns the data, the same
split as udev and `libqhy` on a Linux distribution. machine-operator
sees the bare chip appear, runs the command that a `Firmware` object
names in the image it names, and reports the result on the `Machine`.
This component supplies the image and one `Firmware` object for each
rule.

### The image

`indi/images/qhy` names the files of `indi-qhy`, and `family.sh`
copies the closure of that list, so the image holds only the libraries
the driver loads. The list adds two things:

- `fxload`, from Debian.
- `/lib/firmware/qhy/`, the firmware files that the `libqhy` package
  installs.

Both are a change to the recipe of a pinned component, so `indi` takes
a new revision.

### The `Firmware` objects

`deploy/firmware/qhy.yaml` holds one `Firmware` with `upload` for each
rule in `85-qhyccd.rules`. A generator writes it from the rules file in
the image, the same way `make captures` reads the INDI images, so the
file follows the package at each bump of `indi`:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Firmware
metadata:
  name: qhy-1618-0920
spec:
  selector: |
    device.attributes["liken.sh"].vendor == "1618" &&
    device.attributes["liken.sh"].product == "0920"
  upload:
    image: ghcr.io/liken-sh/indi-qhy:<the tag in drivers/generated.go>
    command: [fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]
```

The generator reads each rule's vendor, product, chip type, firmware
file, and second-stage loader, and refuses a rule of any other form.
A test holds each object's image to the tag of `indi-qhy` in
`drivers/generated.go`, so the objects and the drivers never name two
releases of the image. The operator's install applies the file with
the CRDs, so a cluster that runs this operator loads every QHY camera
on every machine, with or without a reservation.

### The operator

The operator does not change. `PowerOn` switches on a camera's power
output, the bare chip appears, machine-operator loads it, and the
camera appears. The camera's pod stays `Pending` until then, and
`StartDevices` already waits for it.

### The guides and skills

The guides are the manual, and `make -C docs generate skills` writes
each guide as an Agent Skill, so the guides and their skills change in
the same commit:

- `connect-usb-equipment` and its skill move QHY cameras to the
  equipment that works. They say that the operator's install includes
  a `Firmware` object for each QHY model, and that the camera's claim
  selects the loaded camera's product ID, not the bare chip's. They
  link to `liken`'s `device-firmware` guide for the mechanism.
- `troubleshoot` and its skill add a QHY camera whose pod stays
  `Pending` in `StartDevices`: read `status.hardware.firmware.uploads`
  on the machine's `Machine`.
- `install` lists `deploy/firmware/` among what the install applies,
  and says that `liken`'s milestone 87 must be on the cluster for it.

## Decisions

- **The load is `liken`'s, not this operator's.** Firmware state
  belongs to the device on its machine, not to a session. A step in
  `StartDevices` would load a camera only during a reservation, would
  need to read `ResourceSlice`s to tell a loaded camera from an absent
  one, and would be written again by every other component that runs a
  device of this kind.
- **Every rule ships.** The objects cost nothing on a cluster with no
  QHY camera, and a person with another QHY model needs no extra step.
- **The loader runs as user 1000.** `liken` gives a claimed device node
  to the pod's user, so `fxload` needs no root. The proof ran as root,
  so milestone 87's drill measures this.

## Drills

On the test cluster, with the QHY5L-II-C, after milestone 87 is built:

- A cold power cycle of the camera loads it with no person acting.
- A reservation that starts with the camera off powers it, loads it,
  and connects the guide camera in `StartDevices`.
- A power cycle of the camera during a reservation.

## Open questions

- **The license of the firmware files.** INDI's `libqhy.spec` declares
  LGPLv2 for the package and refers to a `COPYRIGHT` file for the
  files it covers. Nobody has read that file, so it is not known
  whether the firmware is under it. `indi/sdk-licenses/` has no entry
  for `libqhyccd` either.
- **FX3 and second-stage cameras.** Only an FX2 camera was measured.
  The FX3 rules and the eleven rules with `fxload -s` are generated by
  the same code and are not proven on hardware.
