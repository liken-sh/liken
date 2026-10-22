# Plans

This directory holds the design documents of `firmware-operator`, the
add-on that loads and updates the firmware of the devices on `liken`
machines. Root [plan 81](../../plans/81-firmware.md) records which
component owns each kind of firmware.

Each `plans/` directory numbers its own plans. A new plan takes the
next number after the highest one in this directory and its
subdirectories. A number never changes. Open problems have no number.

The form follows the plans of the OS in `liken/plans/`. A document
states a problem, states the design that answers it, and states what
was considered and set aside. It separates what was measured from what
was only read, and it names where the measurement ran.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open, and it does not become an open problem. The
header states which drills have not run. The built part closes, and
any work still unbuilt becomes a new plan or an open problem.

## The component

`firmware-operator` is one `Deployment` for the cluster, like
cluster-operator, with leader election under root plan 80. It runs in
`liken-system` and watches every machine. It touches no device itself:
each operation on a device is a short pod that the operator creates on
the device's node. An upload pod holds a claim on that one device. A
scan pod of plan 03 is privileged and holds no claim, because fwupd
reads many devices, and it only reads.

It ships apart from the OS, like display-operator and the other
hardware operators, and a person installs it from its own manual. In
the CI graph it is on the same layer as those operators. Its
`package.toml` depends on `kubernetes`, `notices`, and `trust`, and its
docs prefix is `firmware`. It depends on `trust` because the operator
and the scan pods read a `FirmwareCatalog` over HTTPS. The operator verifies
the API server with the CA of its `ServiceAccount`.

## The plans

| Plan | Status |
|---|---|
| [01](completed/01-uploads.md) | Built: the `Firmware` resource and its `upload` action, which writes firmware into a device each time the device appears |
| [02](rejected/02-updates.md) | Replaced by plans 03, 04, and 05 before it was built |
| [03](completed/03-discovery.md) | Built: the `FirmwareCatalog` resource, the scan pod that runs fwupd on each machine, and a discovered `Firmware` for each catalog component with the releases a catalog offers for it |
| [04](completed/04-approval.md) | Built: `spec.update.version` and `spec.update.cabinet`, a person's approval of one release pinned to its cabinet, with the `Approved` condition and the count of pending machines |
| [05](completed/05-rehearsal.md) | Built: a rehearsal of each approved install against a copy of the machine's firmware variables, with the `Rehearsed` condition |
| [06](completed/06-applying-a-database-update.md) | Built: the write of a Secure Boot database update inside a reboot turn that the OS grants and holds, with the `Applied` condition |
| [07](07-rehearsing-a-capsule.md) | Proposed: the rehearsal of a capsule, with the ESP of the next boot |
| [08](08-applying-a-capsule.md) | Proposed: the write of a capsule inside a turn, with the one-shot boot that the OS hands over |
