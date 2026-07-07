# liken

**liken** *(v.)* — to represent one thing as similar to another; to compare.

Homophone of *lichen*: a symbiont of two organisms living as one, on bare rock.

liken is an experiment in building a Linux distribution that uses Kubernetes
as its service manager. **Li**nux + **K**ubernetes. The name also describes
how it works: a reconciler continuously *likens* the machine to a desired
state declared in git.

## The idea

The OS image acts as a bootloader for a git repo.

The immutable image contains only a kernel, a tiny init stub, and
[k3s](https://k3s.io). Everything else (system services, user apps, node
configuration) is a [Flux](https://fluxcd.io) Kustomization reconciled from
a git repository. A machine's identity is nothing more than the repo and
path it reconciles from.

Some things fall out of that naturally:

* **Backups get simpler.** If all configuration lives in git, there is
  nothing to back up except data volumes. There is no need to snapshot
  `/etc`.
* **Updates are commits.** Flux's image automation keeps apps current; OS and
  kernel upgrades go through
  [system-upgrade-controller](https://github.com/rancher/system-upgrade-controller),
  so even a kernel bump is a git commit.
* **Nodes share container images.** k3s's embedded
  [Spegel](https://spegel.dev) registry mirror lets nodes share images
  peer-to-peer, so re-pulls come from the LAN and keep working even when
  the internet is down.
* **System and user apps are just directories.** The same repo layout that
  works for a homelab cluster works for the machine itself.

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

This is a spike. Nothing works yet; there may not even be code here yet.
It is first and foremost a learning project: the goal is to understand what
an init system actually does by building the smallest one that can stand up
Kubernetes, then letting Kubernetes do the rest. The rough plan is in
[TODO.md](TODO.md).

## License

MIT
