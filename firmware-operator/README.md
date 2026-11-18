# firmware-operator

A `Firmware` is a rule that writes firmware into the USB devices of a
[`liken`](https://liken.sh/) cluster that keep no firmware of their own. This
directory contains the definition of that resource,
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
FX2 or FX3 chip with no program, and the host must upload the program
each time the device gets power. When a device that a rule matches
appears in a machine's `ResourceSlice`, the operator starts a short pod
on that machine with a claim on that device, and writes the result into
the rule's status. The firmware stays in the vendor's image and is
never part of the OS image.

The operator also finds updates for firmware that devices keep across a
power cycle, such as a board's UEFI firmware and the Secure Boot
revocation list. A `FirmwareCatalog`, such as the `lvfs` object that
`deploy/` ships, points at a source of firmware releases. On each
machine the operator runs a privileged scan pod that only reads, and
records each updatable device, the version it runs, and the releases
the catalog offers in a `Firmware` with `spec.update`. Discovery
installs nothing. When you approve a release by setting
`spec.update.version` and `spec.update.cabinet`, the operator rehearses the install on each
machine, and then writes the update during that machine's reboot turn.

The manual is at [liken.sh/firmware](https://liken.sh/firmware/).
The `plans/` directory holds the design, and `make test` runs every
check that CI runs.
