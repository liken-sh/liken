# 81, Firmware

Proposed on 2026-10-19. Steps 1 to 3 of [Order](#order) were built on
2026-10-19, except the report of missing kernel firmware, and the test
cluster's guide camera loaded with no hand-run `Job` on 2026-10-20.
Steps 4 to 6 are not built. This plan records which component
owns each part of firmware in `liken`. The component plans hold the
design.

## Three kinds of firmware

| Kind | Example | Owner | Plan |
|---|---|---|---|
| The kernel loads it from the image | i915 and wifi blobs | the OS image | `liken` [milestone 32](../liken/plans/completed/32-hardware-support-in-the-image.md), built |
| The host uploads it each time a device appears | a QHY guide camera on a bare FX2 chip | `firmware-operator`, a `Firmware` with `upload` | `firmware-operator` [plan 01](../firmware-operator/plans/completed/01-uploads.md) |
| The device keeps it across a power cycle | UEFI, a dock, an SSD | `firmware-operator`, a `Firmware` with `update` | `firmware-operator` plans [03](../firmware-operator/plans/completed/03-discovery.md), [04](../firmware-operator/plans/completed/04-approval.md), and [05](../firmware-operator/plans/05-applying-updates.md), and `liken` [milestone 33](../liken/plans/33-firmware-updates.md) for the boot chain |

## The boundaries

**The OS publishes facts and guards the boot chain.** It knows nothing
about vendor firmware. `liken`'s
[milestone 87](../liken/plans/87-firmware-facts-a-machine-publishes.md)
adds the USB bus and device numbers to each device in a machine's
`ResourceSlice`, and reports in `status.hardware.missingFirmware` the
files that the kernel requested and did not find. Milestone 33 keeps
`init` the only writer of the boot chain, with an efivars shim for
fwupd, and gives a component a rollout turn for a machine. Nothing in
the OS reads a `Firmware` object.

**`firmware-operator` owns the rest.** It is an add-on that ships
apart from the OS, like display-operator, and it is on the same layer
of the CI graph as the other hardware operators. It is one
`Deployment` for the cluster, like cluster-operator, and it touches no
device itself. It owns the cluster-scoped `Firmware` resource in
`firmware.liken.sh`, watches every machine's `ResourceSlice`, creates
the short pods that load or flash one device on its node, reads the
LVFS catalog and proposes updates for a person to adopt, and writes
all of each `Firmware` object's status. A cluster without it loses no
function of the OS.

**A component that runs a device ships the device's `upload` rules**
next to the image that holds the firmware bytes. observatory-operator
[plan 23](../observatory-operator/plans/completed/23-firmware-before-the-driver.md)
ships one for each rule in INDI's `85-qhyccd.rules`, in a
kustomization of its own that a cluster applies when it runs
`firmware-operator`.

The firmware bytes for an upload stay in the vendor's image, and the
bytes for an update come from a cabinet that the `Firmware` pins by
digest. Neither kind enters the OS image.

## Order

1. `liken` milestone 87: the USB attributes and the report of missing
   kernel firmware.
2. `firmware-operator` plan 01: the component, its manual, the
   `Firmware` resource, and uploads.
3. observatory-operator plan 23: the QHY rules and the `indi-qhy`
   image. Then the test cluster's guide camera loads with no hand-run
   `Job`.
4. `firmware-operator` plan 03: discovery of the firmware that devices
   keep and of the releases for it, which installs nothing.
5. `firmware-operator` plan 04: approval of one version.
6. `liken` milestone 33's prerequisites and the shim, then
   `firmware-operator` plan 05, which applies an approved version.

Each step changes its component's guides, and the skills generated
from them, in the same commit.
