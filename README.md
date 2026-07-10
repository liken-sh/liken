# liken

**liken** *(v.)* — to represent one thing as similar to another; to compare.

Homophone of *lichen*: a symbiont of two organisms living as one, on bare rock.

liken is an experiment in building a Linux distribution that uses Kubernetes
as its service manager. **Li**nux + **K**ubernetes. The name also describes
how it works: a reconciler continuously *likens* the machine to a desired
state declared in git.

## The idea

The OS image acts as a bootloader for a git repo.

The immutable image carries the whole operating system: a kernel, liken's
own init (the Go program the kernel runs as PID 1), [k3s](https://k3s.io),
and the handful of host programs a Kubernetes node can't get from a
container — the operators and log relays that run liken itself, mke2fs
for claiming blank disks, the iSCSI and NFS client binaries, and a CA
trust store. There is no shell, no package manager, and no libc;
everything else runs as a container.

Some things fall out of that naturally:

* **Backups get simpler.** If all configuration lives in git, there is
  nothing to back up except data volumes. There is no need to snapshot
  `/etc`.
* **Upgrades are declarative.** The Cluster resource carries a catalog of
  releases and one target version. Machines download the target, verify
  every byte against pinned digests, and write it into the spare slot of
  an A/B pair; a rollout conductor then grants reboots one machine at a
  time, so the fleet never risks its quorum. Upgrading the OS, kernel
  and all, is one field edit.
* **Nodes share container images.** k3s's embedded
  [Spegel](https://spegel.dev) registry mirror lets nodes share images
  peer-to-peer, so re-pulls come from the LAN and keep working even when
  the internet is down.

The layer that completes the idea — system services, user apps, and node
configuration as a [Flux](https://fluxcd.io) Kustomization reconciled
from a git repository, so a machine's identity is nothing more than the
repo and path it reconciles from — is not built yet. That is the plan in
[plans/14-gitops-from-first-boot.md](plans/14-gitops-from-first-boot.md).

## Prior art

This idea isn't new, and these projects explore similar ground:

* [Talos Linux](https://www.talos.dev) has no systemd, no shell, and no
  SSH; you manage the machine entirely through a gRPC API. It is the
  closest thing to this idea that you can run in production today.
* [k3OS](https://github.com/rancher/k3os) was Rancher's distro built on the
  idea that the OS is just enough to run k3s. It is almost exactly this
  idea, but the project is now archived.
* [Kairos](https://kairos.io) is the successor to k3OS in spirit: an
  immutable meta-distro for edge Kubernetes.
* [LinuxKit](https://github.com/linuxkit/linuxkit) and
  [Bottlerocket](https://github.com/bottlerocket-os/bottlerocket) are
  minimal immutable hosts where everything interesting runs in containers.

None of them are quite *GitOps-native from first boot*, though. In each of
them, the git repo is a layer you add on top of the OS. Here, the git repo
defines the OS itself.

## Status

liken boots machines, so far virtual ones: the milestones in
[plans/](plans/) walk from a bare PID 1 to a five-node HA cluster
with declarative upgrades, automated rolling reboots, and adoption of
existing k3s clusters, each proven in the QEMU lab. It is not yet a
public distribution: there are no public releases, and it has never
run on bare metal. Getting there is the current direction; see
[plans/22-public-releases.md](plans/22-public-releases.md). The
design overview and the milestone-by-milestone story live in
[plans/](plans/).

## License

MIT
