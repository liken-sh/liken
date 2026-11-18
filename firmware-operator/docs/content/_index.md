---
title: firmware-operator
---

# `firmware-operator`

`firmware-operator` uploads firmware to the USB devices of a
[`liken`](https://liken.sh/docs/) cluster that lose it at power off.
Many astronomy cameras, logic analyzers, and lab instruments start as
a bare Cypress FX2 or FX3 chip, and the host must upload the device's
program each time the device gets power. On a Linux desktop a udev
rule runs `fxload` when the chip appears. A `liken` machine runs no
udev, so you write the same rule as a `Firmware`, and the operator
runs the upload on whichever machine the device appears on.

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
operator starts a short pod on that machine with a claim on the
device, and writes the result to the `Firmware`'s status. The camera
then reconnects as the real device, and the pod that uses the camera
claims it as usual.

The operator also finds updates for firmware that a device keeps
across a power cycle, such as a board's UEFI firmware, an SSD's
firmware, or the Secure Boot revocation list. A `FirmwareCatalog` is a
source of firmware releases, and the base installs one for the Linux
Vendor Firmware Service (LVFS). A read-only scan pod on each machine
reports each device, the version it runs, and the newer releases that
the catalog offers. The operator records them in one `Firmware` for
each catalog component, such as `microsoft-dbx-x64` for the
revocation list. To approve a release, you set its version and the
URL and SHA-256 of its cabinet on that `Firmware`. The operator
installs an approved update of a Secure Boot database or a UEFI
capsule, such as a board's firmware, during each machine's reboot
turn. It refuses an update that another fwupd plugin installs, such as
an SSD's.

```sh
kubectl get firmware -l firmware.liken.sh/discovered
```

Start here:

* [Install](/docs/guides/install/) the operator.
* [Upload firmware to a device](/docs/guides/upload-to-a-device/):
  write a `Firmware` and read what it did.
* [Find firmware updates](/docs/guides/find-updates/): read which
  updates the catalog offers for each machine.
* [Approve a firmware update](/docs/guides/approve-an-update/): pin
  the release you want and read which machines wait for it.
* [Troubleshoot](/docs/guides/troubleshoot/) an upload or a scan that
  failed or did not start, or an approval that is `False`.
* [Reference](/docs/reference/firmware/): every field of a
  `Firmware` and a [`FirmwareCatalog`](/docs/reference/firmwarecatalog/).

The operator is released separately from the OS. The OS publishes each
machine's devices in its `ResourceSlice`, and the operator reads them
there. [How uploads work](/docs/concepts/uploads/) and
[How discovery works](/docs/concepts/discovery/) explain what the
operator does and why.

* [The source](https://github.com/liken-sh/liken/tree/main/firmware-operator)
* [The `liken` manual](https://liken.sh/docs/)
