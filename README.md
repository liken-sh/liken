# `liken`

<img src="brand/liken.svg" alt="The liken mark: a patch of lichen drawn as hexagonal tiles" width="130" align="right">

**liken** *(v.)* — to represent one thing as similar to another; to compare.

Homophone of *[lichen](https://en.wikipedia.org/wiki/Lichen)*: a symbiont
of two organisms living as one, and one of the first things to colonize
bare rock.

The icon is a patch of that lichen, drawn as the polygonal plates
(*[areoles](https://en.wikipedia.org/wiki/Crustose_lichen)*) that a
crustose lichen cracks into as it grows. [`brand/`](brand/) explains
the design and the biology behind it.

`liken` is a small operating system that boots a machine straight into
Kubernetes and uses it as the service manager. **Li**nux +
**K**ubernetes. The name also describes how it works: a reconciler
continuously *likens* the machine to its declared state.

## The idea

The immutable image carries the whole operating system. It has a kernel,
`liken`'s own init (the Go program the kernel runs as PID 1), and
[`k3s`](https://k3s.io). It also has a small number of host programs that
a Kubernetes node cannot get from a container. These programs are the
operators and log relays that run `liken` itself, `mke2fs` for the setup
of blank disks, the iSCSI and NFS client binaries, and a CA trust store.
The image has no shell, no package manager, and no libc. Everything else
runs as a container.

Some results follow from this naturally:

* **Backups get simpler.** If all configuration lives in git, you back up
  only the data volumes. You do not need to make a snapshot of `/etc`.
* **Upgrades are declarative.** The `Cluster` resource carries a catalog
  of releases and one target version. Each machine downloads the target
  release, checks every byte against pinned digests, and writes the
  release into the spare slot of an A/B pair. A rollout conductor then
  allows one machine to reboot at a time. This order keeps the fleet's
  quorum safe. To upgrade the OS, kernel included, you edit one field.
* **Nodes share container images.** `k3s` includes the
  [Spegel](https://spegel.dev) registry mirror. Spegel lets nodes share
  images with each other directly, so re-pulls come from the LAN and
  keep working even when the internet connection is down.

The project has not built the layer that completes this idea yet. That
layer will treat system services, user apps, and node configuration as a
[Flux](https://fluxcd.io) `Kustomization`, reconciled from a git
repository. Then a machine's identity will be nothing more than the
repository and path it reconciles from. See
[plans/14-gitops-from-first-boot.md](plans/14-gitops-from-first-boot.md)
for this plan.

## Prior art

This idea is not new. These projects explore similar ground:

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

None of these projects are GitOps-native from first boot. In each one,
the git repository is a layer added on top of the OS. `liken` is moving
in that direction too, but it is not there yet.

## Status

`liken` runs in public. A `liken` cluster serves
[liken.sh](https://liken.sh) from a 1 GB cloud node. That node installed
itself from the project's release channel, and it still upgrades itself
from the channel. The channel,
[releases.liken.sh](https://releases.liken.sh/channel.yaml), serves
digest-verified releases. CI publishes a new release on every version
tag. [GETTING-STARTED.md](GETTING-STARTED.md) describes the path from a
release to a running cluster of your own.

The milestones in [plans/](plans/) record the project's progress so far.
Each one was proven in the QEMU lab, from a bare PID 1 to a five-node HA
cluster. Later milestones added declarative upgrades, rolling reboots,
adoption of existing `k3s` clusters, and updates straight from the
internet, under both UEFI and BIOS firmware. `liken` has never run on
bare metal, and the GitOps layer described above is not built yet.
[plans/](plans/) also holds the design overview and the plan for what
comes next.

## License

Everything in this repository is `liken`'s own work, under the MIT
license. The build fetches the kernel, `k3s`, and the other vendored
components at build time. The repository never commits them, so it
carries no third-party code.

A built release does redistribute those components, each under its own
license. Every release bundles a `LICENSES.md` file that names each
component, its license, and its copyright. The release channel also
serves the source of every copyleft component beside the binaries built
from it. The [`./licensing/` directory](./licensing/) explains how the
project meets these obligations.
