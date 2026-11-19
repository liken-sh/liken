# `liken`

<img src="https://liken.sh/brand/liken.svg" alt="The liken mark: a patch of lichen drawn as hexagonal tiles" width="130" align="right">

**liken** *(v.)*: to represent one thing as similar to another; to compare.

Homophone of *[lichen](https://en.wikipedia.org/wiki/Lichen)*: a symbiont
of two organisms living as one, and one of the first things to colonize
bare rock.

The icon is a patch of that lichen, drawn as the polygonal plates
(*[areoles](https://en.wikipedia.org/wiki/Crustose_lichen)*) that a
crustose lichen cracks into as it grows. The
[brand component](../brand/) explains the
design and the biology behind it.

`liken` is a small operating system that boots a machine directly into
Kubernetes and uses Kubernetes as the service manager. **Li**nux +
**K**ub**e**r**n**etes. The name also describes the operation: a reconciler
makes the machine like its declared state.

## What is in the image

The immutable image contains the full operating system: a kernel,
`liken`'s own init (the Go program that the kernel runs as PID 1), and
[`k3s`](https://k3s.io). It also contains a few host programs
that a Kubernetes node cannot get from a container. These are
the operators and log relays that run `liken` itself, `mke2fs` to format
blank disks, the iSCSI and NFS client binaries, and a CA trust store. The
image has no shell, no package manager, and no libc. All other software
runs in a container.

## What this makes possible

* **Backups are smaller.** If all configuration is in git, you back up
  only the data volumes. You do not make a snapshot of `/etc`.
* **Upgrades are declarative.** The `Cluster` resource holds a catalog
  of releases and one target version. Each machine downloads the target
  release, compares every byte against pinned digests, and writes the
  release into the spare slot of an A/B pair. `cluster-operator` then
  lets one machine at a time reboot, so the fleet keeps its quorum. To
  upgrade the operating system and the kernel, you change one field.
* **Nodes share container images.** `k3s` includes the
  [Spegel](https://spegel.dev) registry mirror, which lets nodes send
  images to each other directly. A node pulls an image that another
  node already has over the LAN, so that pull works when the internet
  connection is down.
* **The cluster syncs itself from git.** Declare a git repository on the
  `Cluster` resource, and the cluster syncs it with
  [Flux](https://fluxcd.io). The repository holds the Cluster document,
  the Machine documents, and the workloads, and the fleet converges to
  each commit. The cluster generates its own deploy key, so no private
  key leaves the cluster. [The GitOps
  guide](https://liken.sh/docs/guides/gitops/) gives the steps, and
  [plans/completed/14-gitops-from-first-boot.md](plans/completed/14-gitops-from-first-boot.md)
  records the design.

## How liken is written

liken is written to be read. The comments in the shell scripts, the
manifests, and the Go code do more than say what a line does. They
teach the domain: why the kernel does not mount `/proc` on its own, why
`k3s` needs cgroups, and why an initramfs is a cpio archive. A person
who reads `liken/` from top to bottom learns how a Linux system
boots, and how Kubernetes takes control after that.

This is why files here contain more commentary than the same files
would contain in another project. The commentary is deliberate, and it is
the documentation. The idea is
[literate programming](https://en.wikipedia.org/wiki/Literate_programming),
which Donald Knuth described in 1984. His original form builds the
program and the document from one source, through a tool. liken does
not do that. It keeps the goal, which is a repository that explains
itself, and it uses ordinary files that run as they are.

An explanation that is too big for a comment goes in a markdown
document beside the thing it describes. [`plans/`](plans/) contains the
design overview and one document for each milestone of the OS. A plan
that spans more than one component is in [`plans/`](../plans/) at the top
of the repository.

## Prior art

These projects explore similar ground:

* [Talos Linux](https://www.talos.dev) has no `systemd`, no shell, and no
  SSH. You manage the machine only through a gRPC API. Of the projects
  listed here, it comes closest to this idea, and you can run it in
  production today.
* [k3OS](https://github.com/rancher/k3os) was Rancher's distribution,
  built on the idea that the OS should do no more than run `k3s`. This is
  almost the same idea as `liken`, but the project is now archived.
* [Kairos](https://kairos.io) continues the k3OS idea: an immutable
  meta-distribution for edge Kubernetes.
* [LinuxKit](https://github.com/linuxkit/linuxkit) and
  [Bottlerocket](https://github.com/bottlerocket-os/bottlerocket) are
  minimal, immutable hosts. Almost every program on them runs in a
  container.

None of these projects syncs from git from its first boot. In each
one, you add the git sync on top of the OS. In `liken`, the git
repository is a feature of the `Cluster` resource, so a new cluster can
sync from its first boot.

## Status

`liken` is public. The release channel,
[releases.liken.sh](https://releases.liken.sh/), serves
digest-verified releases, and CI publishes a new release on every
version tag. [GETTING-STARTED.md](GETTING-STARTED.md) describes the
path from a release to a running cluster of your own.

`liken` runs on the QEMU guests of [`dev-cluster/`](dev-cluster/) and
on physical machines. The guest in
[`dev-cluster/hardware/`](dev-cluster/hardware/) has a SATA controller
and a real network card. The other guests use paravirtual disks and
network cards, which the kernel drives without loading a module, so
they cannot show a fault in a driver that the image loads.
[Milestone 32](plans/completed/32-hardware-support-in-the-image.md)
lists what only a physical machine can prove.

The milestones in [`plans/`](plans/) record the design of the OS:

* [`plans/completed/`](plans/completed/) contains the milestones that are
  built.
* [`plans/rejected/`](plans/rejected/) contains the milestones that the
  project rejected, with the reasons.
* [`plans/open-problems/`](plans/open-problems/) records bugs and design
  problems that are not solved.
* The markdown files in [`plans/`](plans/) are the design overview and
  the proposals that are not built.

## License

Everything in `liken/` is `liken`'s own work, under the MIT license.
The build fetches the kernel, `k3s`, and the other vendored components
at build time. `liken/` never commits them, so it contains no
third-party code.

A built release does redistribute those components, each under its own
license. Every release bundles a `LICENSES.md` file that names each
component, its license, and its copyright. The release channel also
serves the source of every copyleft component beside the binaries built
from it. The [`./licensing/` directory](./licensing/) explains how the
project meets these obligations.
