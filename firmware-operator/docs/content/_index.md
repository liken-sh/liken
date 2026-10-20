---
title: firmware-operator
---

# `firmware-operator`

`firmware-operator` writes firmware into the USB devices of a
[`liken`](https://liken.sh/docs/) cluster that hold none of their own.
Many astronomy cameras, logic analyzers, and lab instruments power up
as a bare Cypress FX2 or FX3 chip, and the host must upload the
device's program into the chip each time it gets power. On a Linux
desktop a udev rule runs `fxload` when the chip appears. A `liken`
machine runs no udev, so you declare the same rule as a `Firmware`,
and the operator runs the upload on whichever machine the device
appears on.

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: qhy5l-ii
spec:
  selector: |
    device.attributes["liken.sh"].vendor == "1618" &&
    device.attributes["liken.sh"].product == "0920"
  upload:
    image: ghcr.io/liken-sh/indi-qhy:<tag>
    command: [fxload, -t, fx2, -I, /lib/firmware/qhy/QHY5II.HEX, -D, $(LIKEN_DEVICE)]
```

When a matching device appears in a machine's `ResourceSlice`, the
operator starts one short pod on that machine, with a claim on that
one device, and writes the result into the `Firmware`'s status. The
camera then enumerates again as the real device, and the pod that
uses it claims it the usual way.

The operator also finds updates for the firmware that devices keep
across a power cycle, such as a board's UEFI firmware or an SSD. A
`FirmwareCatalog` names a source of releases, and the base installs the one
for the Linux Vendor Firmware Service (LVFS). The operator runs a short
read-only scan pod on each machine, and records each device, the
version it runs, and the newer releases the catalog offers in a
`Firmware` for that device model. It installs none of them.

```sh
kubectl get firmware -l firmware.liken.sh/discovered
```

Start here:

* [Install](/docs/guides/install/) the operator.
* [Upload firmware to a device](/docs/guides/upload-to-a-device/):
  write a `Firmware` and read what it did.
* [Find firmware updates](/docs/guides/find-updates/): read which
  updates the catalog offers for each machine.
* [Troubleshoot](/docs/guides/troubleshoot/) an upload or a scan that
  failed or never started.
* [Reference](/docs/reference/firmware/): every field of a
  `Firmware` and a [`FirmwareCatalog`](/docs/reference/firmwarecatalog/).

The operator ships apart from the OS: the OS publishes each machine's
devices, and this operator reads them. Only the upload pods write to a
device, and a scan pod only reads. [How uploads work](/docs/concepts/uploads/)
and [How discovery works](/docs/concepts/discovery/) explain the rules
it follows.

* [The source](https://github.com/liken-sh/liken/tree/main/firmware-operator)
* [The `liken` manual](https://liken.sh/docs/)
