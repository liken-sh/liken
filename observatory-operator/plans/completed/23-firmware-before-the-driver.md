# 23, Firmware before the driver

Proposed on 2026-10-19. Built on 2026-10-19, in `indi` revision 7,
`20261005-7`, and in `deploy/firmware/qhy.yaml`, with 47 `Firmware`
objects. The build left out the FX3 chips and two generic chips, for
the reasons under
[What the build found](#what-the-build-found). The first drill below
passed on liken-1 on 2026-10-20, and the two drills with a reservation
have not run. `firmware-operator`'s
[plan 01](../../../firmware-operator/plans/completed/01-uploads.md) adds the
`Firmware` resource and the pods that write firmware into a device
when it appears. This plan ships the `Firmware` objects for QHY
cameras and the files they need in `indi-qhy`. The operator's own code
does not change. Root [plan 81](../../../plans/81-firmware.md) records
the boundary between the components.

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
[a replugged USB device](../open-problems/a-replugged-usb-device.md),
where the same device comes back.

## The design

`firmware-operator` owns the mechanism and this component owns the
data, the same split as udev and `libqhy` on a Linux distribution.
`firmware-operator` reads the bare chip from the machine's
`ResourceSlice`, runs the command that a `Firmware` object names in
the image it names, and reports the result in the `Firmware` object's
status. This component supplies the image and one `Firmware` object
for each rule.

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
apiVersion: firmware.liken.sh/v1alpha1
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
releases of the image. `deploy/firmware/` is its own kustomization,
because the `Firmware` CRD comes from `firmware-operator`. A cluster
that runs both operators applies it, and then loads every QHY camera
on every machine, with or without a reservation. A cluster without
`firmware-operator` runs every other device, and its QHY cameras stay
bare.

### The operator

The operator does not change. `PowerOn` switches on a camera's power
output, the bare chip appears, `firmware-operator` loads it, and the
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
  link to `firmware-operator`'s `firmware:upload-to-a-device` for the
  mechanism.
- `troubleshoot` and its skill add a QHY camera whose pod stays
  `Pending` in `StartDevices`: read the status of the camera's
  `Firmware` object.
- `install` says to install `firmware-operator` and apply
  `deploy/firmware/` for QHY cameras.

## What the build found

The build read the rules file and the packages of the snapshot of
2026-10-05, and found five facts that the design above did not state:

- **Ubuntu's `fxload` does not load FX3 chips.** `fxload`
  0.0.20081013-2ubuntu2 names the chip types `an21`, `fx`, `fx2`, and
  `fx2lp` in its usage text, and refuses `-t fx3` with "illegal
  microcontroller type". No other package of the snapshots is named
  `fxload`. So the generator
  writes no object for a rule with `-t fx3`, and a QHY camera that
  powers up as a bare FX3 chip stays bare. The rules name 65 such
  chips. A loader for them is the open problem
  [QHY cameras on FX3 chips](../open-problems/qhy-cameras-on-fx3-chips.md).
- **116 of the 117 rules run.** One rule, for `1618:c411` with
  `-t fx3`, is a comment. The 116 are 18 for FX2 chips, 32 for FX2LP,
  and 66 for FX3, and 11 of them name `fxload -s`.
- **Two chips have more than one rule.** The rule for `1618:0482` is
  in the file twice, word for word. For `0547:1002`, one rule writes
  `QHY5LOADER.HEX`, a second sleeps 5 seconds, and a third writes
  `QHY5.HEX`. A pod runs one command, so that chip takes
  `fxload -s QHY5LOADER.HEX`, the form that the rules give every other
  chip that loads `QHY5.HEX`. The 116 rules name 114 chips: 49 that
  the image's `fxload` loads, 12 of them with `-s`, and 65 FX3 chips.
- **The files are under `/usr/lib/firmware/qhy/`.** The package
  installs them there, and the rules name that path. The image keeps
  Ubuntu's link from `/lib` to `usr/lib`, so the path of the design
  above names the same files. `QHY492.img`, which the FX3 rule for
  `1618:c492` names, is not in the package.
- **Two rules match generic Cypress chips.** `04b4:8613` is the ID of
  a Cypress FX2 with no EEPROM, and `0547:1002` the ID of an Anchor
  Chips EZ-USB. The rules load `QHY5.HEX` into either, as they do on
  any Linux host with `libqhy`. On a cluster that applies
  `deploy/firmware/`, another device that powers up with one of these
  IDs, such as a logic analyzer with no EEPROM, takes the QHY5's
  firmware. So the generator leaves both out, and the 49 chips that
  the image's `fxload` loads give 47 objects.

The license of the firmware files is still open. `libqhy/` at the
pinned commit of indi-3rdparty holds no `COPYRIGHT` or license file,
and neither does the top of the repository, which holds `LICENSE`, the
text of the LGPL 2.1. So the `COPYRIGHT` file that `libqhy.spec`
refers to does not exist beside it. The copyright file of
`indi-3rdparty-libs`, the package that installs the files, states
Apache-2.0 for every file and names no vendor SDK.
`debian/libqhy/copyright`, the copyright file of a Debian package that
the snapshot of the PPA does not serve, names "QHY CCD
<lzr@qhyccd.com>" as the copyright holder and gives the text of
Creative Commons Attribution 3.0 Unported. `indi-qhy` holds both
files. No file names the firmware files themselves.

## Decisions

- **The load is `firmware-operator`'s, not this operator's.** Firmware state
  belongs to the device on its machine, not to a session. A step in
  `StartDevices` would load a camera only during a reservation, would
  need to read `ResourceSlice`s to tell a loaded camera from an absent
  one, and would be written again by every other component that runs a
  device of this kind.
- **Every rule ships.** The objects cost nothing on a cluster with no
  QHY camera, and a person with another QHY model needs no extra step.
- **The loader runs as user 1000.** `liken` gives a claimed device node
  to the pod's user, so `fxload` needs no root. The proof ran as root,
  so the drill of `firmware-operator`'s plan 01 measures this. It
  passed on 2026-10-20.

## Drills

On the test cluster, with the QHY5L-II-C, after `firmware-operator`'s
plan 01 is built:

- A cold power cycle of the camera loads it with no person acting.
- A reservation that starts with the camera off powers it, loads it,
  and connects the guide camera in `StartDevices`.
- A power cycle of the camera during a reservation.

The first drill passed on 2026-10-20, with the `Firmware` from
`qhy-1618-0920` and `indi-qhy` `20261005-7`. The camera was unplugged
and plugged in again, and `firmware-operator` loaded `QHY5II.HEX` with
`fxload` as user 1000 in 4 seconds, with no person acting. The camera
came back as `1618:0921`. The two drills with a reservation have not
run.

## Open questions

- **The license of the firmware files.** INDI's `libqhy.spec` declares
  LGPLv2 for the package and refers to a `COPYRIGHT` file for the
  files it covers. Nobody has read that file, so it is not known
  whether the firmware is under it. `indi/sdk-licenses/` has no entry
  for `libqhyccd` either.
- **Second-stage cameras.** Only an FX2 camera with one stage was
  measured. The rules with `fxload -s` are generated by the same code
  and are not proven on hardware.

The FX3 chips, which have no loader and no object, are the open
problem [QHY cameras on FX3 chips](../open-problems/qhy-cameras-on-fx3-chips.md).
