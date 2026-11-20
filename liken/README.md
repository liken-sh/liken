# `liken`

<img src="https://liken.sh/brand/liken.svg" alt="The liken mark: a patch of lichen drawn as hexagonal tiles" width="130" align="right">

**liken** *(v.)*: to represent one thing as similar to another; to compare.

Homophone of *[lichen](https://en.wikipedia.org/wiki/Lichen)*: two
organisms living as one, and one of the first things to colonize bare
rock. The mark is a patch of lichen, drawn as the plates
(*[areoles](https://en.wikipedia.org/wiki/Crustose_lichen)*) that a
crustose lichen cracks into. [`brand/mark.md`](../brand/mark.md)
explains the design.

`liken` is a small operating system that boots a machine directly into
Kubernetes and uses Kubernetes as its service manager. **Li**nux +
**K**ub**e**r**n**etes. The name also says what the system does: a
reconciler makes each machine like the state you declared for it.

You manage a cluster's machines as you manage its workloads, with
manifests and `kubectl`. So you can keep the configuration in git and
back up only the data volumes. Each machine installs itself, joins the
cluster, and reports its hardware in its status. To upgrade the fleet,
you make one edit to the `Cluster`. If a machine fails to boot the new
release, it goes back to the release it ran before. `liken` runs on
x86-64 machines with UEFI or BIOS firmware.

The OS manages everything on the machine up to and including k3s:
the boot, the disks, the network, the clock, the cluster's identity,
the upgrade of each machine, and the hardware, which it publishes as
devices that a pod can claim. The image contains the Linux kernel,
`liken`'s own init, [k3s](https://k3s.io), and the host programs that
cannot run from a container, such as `mke2fs` and the iSCSI and NFS
clients. It has no shell, no package manager, and no libc. Every other
program runs in a container.

The OS is all that a cluster needs to run. It publishes a monitor or a
sound card as a device, but it does not drive it. An extension
operator, such as `display-operator`, is an optional workload that
claims the device. The OS is built with the `kubernetes/` Go module,
the pins in `toolchains/`, and the CA bundle in `trust/`, and each
release goes to [releases.liken.sh](https://releases.liken.sh/).

## The kinds

- **`Cluster`** describes the fleet: the leaders, the address plan,
  the release catalog and the target version, the registry settings,
  and features. `flux` syncs the cluster from git from its first boot.
- **`Machine`** describes one computer: its interfaces, the purpose
  of each disk, its kernel modules, its sysctls, and its reboot policy.
- **`Disruption`** asks for a turn to reboot a machine or restart k3s.
- **`MachineDisruptionBudget`** sets how many of the machines that it
  selects can be out of service at the same time.

## The programs

- **`init`** is the Go program that the kernel runs as PID 1. It
  prepares the disks, the network, and the clock, and then starts k3s.
- **`machine-operator`** is a privileged pod on each machine. It writes
  the `Machine`'s status, applies its spec, and downloads the target
  release into the boot slot that the machine does not run from. As
  the Dynamic Resource Allocation (DRA) driver `liken.sh`, it
  publishes the machine's devices.
- **`cluster-operator`** writes the `Cluster`'s status, grants the
  reboot turns, and marks a machine `Lost` when its heartbeat stops.
- **`liken`** is the toolkit in each release. It creates the
  cluster's identity, builds the install stick, and verifies a release.

To upgrade, add the release to the catalog and set `version` to it:

    spec:
      version: "<release>"
      releases:
        source: https://releases.liken.sh
        catalog:
          - version: "<release>"
            digest: sha256:<digest of the release's release.yaml>

## How liken is written

The comments in the scripts, the manifests, and the Go code explain
the domain, such as why the kernel does not mount `/proc` on its own.
If you read `liken/` from top to bottom, you learn how a Linux system
boots and how Kubernetes takes control after that. The idea is Knuth's
[literate programming](https://en.wikipedia.org/wiki/Literate_programming),
but in ordinary files that run as they are.

[Talos Linux](https://www.talos.dev), [k3OS](https://github.com/rancher/k3os),
and [Kairos](https://kairos.io) also build an immutable OS for
Kubernetes. [LinuxKit](https://github.com/linuxkit/linuxkit) and
[Bottlerocket](https://github.com/bottlerocket-os/bottlerocket) build
immutable hosts that run almost every program in a container.

The manual is at [liken.sh](https://liken.sh/).
[GETTING-STARTED.md](GETTING-STARTED.md) takes you from a release to a
cluster. [plans/00-design.md](plans/00-design.md) is the design.

MIT. See [LICENSE](LICENSE). A release also redistributes the kernel,
k3s, and other components under their own licenses, which
[licensing/NOTICES.md](licensing/NOTICES.md) lists.
