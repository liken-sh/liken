# firmware-operator

`firmware-operator` loads the firmware of a `liken` cluster's devices
and keeps it up to date. No machine needs a vendor's tool installed,
and no person needs to be at a machine's console.

Some USB devices don't store firmware of their own, so the host must
load it each time the device gets power. The operator loads firmware
into each device that a rule matches, on whichever machine the device
appears. Other firmware stays on the device across a power cycle,
such as a board's UEFI firmware. For that firmware, the operator reads
a catalog of releases and reports the updates that each machine can
install. It installs an update only when you have approved one
release and pinned its file by SHA-256, a rehearsal of the install
has passed on that machine, and the machine is drained in its turn to
reboot. It installs only the kinds of update that it supports, such
as a UEFI capsule, and it rejects any other kind. The manual's page
on discovery names the kinds. The operator ships no firmware.
Each file comes from a container image that you name, or from a
catalog that a `FirmwareCatalog` declares.

The operator needs `liken`. The OS publishes each machine's devices
in its `ResourceSlice`, and it records each machine's boot in its
`Machine`. It gives a machine its turn to drain and reboot through a
`Disruption`. Another operator can ship `Firmware` objects for the
hardware that it runs, as `observatory-operator` does for its
cameras.

## The kinds

- A `Firmware` is a rule for one kind of device. An upload rule is
  for bare devices, which are devices that wait for the host to load
  their firmware. It has a CEL selector that matches those devices,
  and the image and command that load their firmware. An update rule
  covers one firmware component that a catalog offers releases for.
  The operator creates an update rule for each component that a scan
  finds. You approve a release on it with `spec.update.version` and
  `spec.update.cabinet`, which pins the release's `.cab` file. The
  status has an entry for each device that the rule matched or
  covers.
- A `FirmwareCatalog` is a source of firmware releases. The base
  declares one named `lvfs`, for the Linux Vendor Firmware Service. On
  a cluster with no route to the internet, delete it and declare a
  catalog for your own mirror.

## An upload rule

```yaml
apiVersion: firmware.liken.sh/v1alpha1
kind: Firmware
metadata:
  name: example-camera
spec:
  selector: |
    device.attributes["liken.sh"].vendor == "1234" &&
    device.attributes["liken.sh"].product == "5678"
  upload:
    image: registry.example.com/example-camera-firmware:1.0
    command: [fxload, -t, fx2, -I, /firmware/camera.hex, -D, $(LIKEN_DEVICE)]
```

The manual is at [liken.sh/firmware](https://liken.sh/firmware/), and
[`plans/README.md`](plans/README.md) describes the design and lists
the plans.

MIT. See [LICENSE](LICENSE).
