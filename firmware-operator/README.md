# firmware-operator

A `Firmware` is a rule that writes firmware into the USB devices of a
[`liken`](https://liken.sh/) cluster that hold none of their own. This
directory holds the definition of that resource,
`firmware.liken.sh/v1alpha1`, and `firmware-operator`, which runs the
uploads.

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

Many astronomy cameras and lab instruments power up as a bare Cypress
FX2 or FX3 chip, and the host must upload the device's program each
time it gets power. When a device that a rule matches appears in a
machine's `ResourceSlice`, the operator starts one short pod on that
machine with a claim on that one device, and writes the result into
the rule's status. The firmware bytes stay in the vendor's image and
never enter the OS image.

The operator also finds updates for the firmware that devices keep. A
`Catalog`, such as the `lvfs` object that `deploy/` ships, names a
source of firmware releases. The operator runs a short privileged scan
pod on each machine, which only reads, and records each updatable
device, the version it runs, and the releases the catalog offers in a
`Firmware` with `spec.update`. It installs none of them.

The manual is at [liken.sh/firmware](https://liken.sh/firmware/).
The `plans/` directory holds the design, and `make test` runs every
check CI runs.
