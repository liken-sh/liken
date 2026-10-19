# 81, Firmware

Proposed on 2026-10-19. Not built. This plan records which component
owns each part of firmware in `liken`. The component plans hold the
design.

## Three kinds of firmware

| Kind | Example | Owner | Plan |
|---|---|---|---|
| The kernel loads it from the image | i915 and wifi blobs | the OS image | `liken` [milestone 32](../liken/plans/completed/32-hardware-support-in-the-image.md), built |
| The host uploads it each time a device appears | a QHY guide camera on a bare FX2 chip | a `Firmware` with `upload` | `liken` [milestone 87](../liken/plans/87-firmware-a-device-takes-from-the-host.md) |
| The device keeps it across a power cycle | UEFI, a dock, an SSD | a `Firmware` with `update` | `liken` [milestone 33](../liken/plans/33-firmware-updates.md) |

## Who does what

- **`liken` owns the `Firmware` resource.** It is cluster-scoped in
  `liken.sh/v1alpha1`, beside `Machine` and `Cluster`, and selects a
  device model with CEL over the devices that machine-operator
  publishes. Exactly one of `upload` and `update` is set.
- **machine-operator is the only process that touches a device.** On
  each machine, it runs the upload pods and the fwupd pods, and it
  writes the results to that machine's `status.hardware.firmware`.
  `init` takes no part, except the efivars shim that milestone 33
  needs to stay the only writer of the boot chain.
- **`firmware-operator` proposes updates.** It is a new, optional
  component: one `Deployment` for the cluster, like cluster-operator,
  with no access to devices. It reads the LVFS catalog, creates a
  labeled `Firmware` with an `update` that has no `version` for each
  device model that no object covers, and writes all of each
  `Firmware` object's status. A person adopts a proposal by setting
  the version. A cluster without it still runs every upload and every
  update that a person declares.
- **A component that runs a device ships its `upload` rules** next to
  the image that holds the firmware bytes. observatory-operator
  [plan 23](../observatory-operator/plans/23-firmware-before-the-driver.md)
  ships one for each rule in INDI's `85-qhyccd.rules`.

The firmware bytes for an upload stay in the vendor's image, and the
bytes for an update come from a cabinet that the `Firmware` pins by
digest. Neither kind enters the OS image.

## Order

1. `liken` milestone 87: the resource, `upload`, and
   `status.hardware.firmware`, with the report of missing kernel
   firmware.
2. observatory-operator plan 23: the QHY rules and the `indi-qhy`
   image. Then the test cluster's guide camera loads with no hand-run
   `Job`.
3. `liken` milestone 33's prerequisites, then `update` and the fwupd
   pods.
4. `firmware-operator`, in its own directory, with its own plans and
   manual.

Each step changes its component's guides, and the skills generated
from them, in the same commit.
