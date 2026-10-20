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
the device's node, with a claim on that one device.

It ships apart from the OS, like display-operator and the other
hardware operators, and a person installs it from its own manual. In
the CI graph it is on the same layer as those operators. Its
`package.toml` depends on `kubernetes` and `notices`, and its docs
prefix is `firmware`. It does not depend on `trust`, because the
operator makes no TLS call except to the API server, which it
verifies with the CA of its `ServiceAccount`.

## The plans

| Plan | Status |
|---|---|
| [01](completed/01-uploads.md) | Built: the `Firmware` resource and its `upload` action, which writes firmware into a device each time the device appears |
| [02](02-updates.md) | Proposed, a stub: the `update` action, which flashes a new version into a device that keeps it, and the proposals that the operator makes from the LVFS catalog |
