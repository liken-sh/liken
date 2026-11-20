# Working on liken

`liken/` is the OS: one Go module, with a directory for each program,
and the scripts, manifests, and pins that build the image.
`README.md` describes its kinds and its programs, and
`plans/00-design.md` gives the design: the boot archives, the A/B
slots, the machine plane in `init`, and the change that converges by
reboot. Read the relevant numbered plan before you change how
something works.

## How it runs

An installed UEFI machine has no bootloader. `init` writes a boot
entry for each slot, and the firmware starts the kernel's EFI stub
from it directly. A BIOS machine boots through GRUB, and the install
stick boots through systemd-boot.

`init` puts a tmpfs overlay over `liken.sqfs` and copies the
deployment layer onto it before it switches root. The deployment
layer is the manifests and the cluster's identity.

`init` and `machine-operator` pass state through files. `init` writes
the facts it finds about the machine to `/run/liken`, and
`machine-operator` reads them and writes the `Machine`'s status.
`init` reboots the machine only when `machine-operator` writes the
intent file that asks for a reboot. `machine-operator` writes that
file after `cluster-operator` grants the machine's `Disruption` and
the node is drained.

`machine-operator` applies each change to the spec that needs no
reboot, and writes every other change to the machine's disk for the
next boot. It installs a release only when the `Cluster`'s catalog
lists the digest of the release's `release.yaml`. It renews the
machine's heartbeat `Lease`, and `cluster-operator` marks the machine
`Lost` when the `Lease` is stale.

`cluster-operator` changes the cluster only through the Kubernetes
API. It grants each `Disruption` when the `MachineDisruptionBudget`s
allow it. Its one other connection is to the release channel at
`releases.liken.sh`, which it reads to report the newest release.

## Design

Each component of `init` runs as a goroutine in PID 1, as root.
`init` restarts a component that returns an error or panics. It
cannot catch a fatal runtime error, such as concurrent map access or
running out of memory. That error ends PID 1, and then the kernel
panics. The project accepts this because the root filesystem is in
RAM, `init` verifies manifests before it trusts them, and a reboot
returns the machine to a known state. A component belongs in a child
process when it parses untrusted input from the network, needs fewer
privileges than PID 1, or must not take the machine down when it
fails. The comment at the top of `init/components.go` has the rule.

`machine-operator` serves the `liken.sh` DRA driver, which publishes
each machine's hardware as devices. A device's attributes record the
facts about it that no other layer can observe. An extension operator
claims a device
through the standard DRA interfaces, the same ones that work with any
other DRA driver. No code in the OS reads an operator's kinds. When
an operator needs a new fact about a device, the OS adds the fact as
an attribute, and the logic of the feature stays in the operator.

## Finding things

- When you change a CRD's schema, raise its `liken.sh/schema-revision`
  annotation. `TestTheSeededCRDsPinTheirSchemaRevision` in `init/`
  fails when you don't. `init` applies whichever copy of a CRD has the
  higher revision. So a machine that boots an older slot does not
  replace the cluster's schema with an older one.
- The ordinary lab guests have virtio devices, which need no kernel
  module. `dev-cluster/hardware/README.md` describes a lab guest that
  does load its storage and network modules.
- The scripts in `licensing/` write each release's `LICENSES.md` from
  `licensing/NOTICES.md`. `licensing/sources.sh` copies the source of
  each copyleft component to the release channel.
