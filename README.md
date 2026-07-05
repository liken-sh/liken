# liken

**liken** *(v.)* — to represent one thing as similar to another; to compare.

Homophone of *lichen*: a symbiont of two organisms living as one, on bare rock.

liken is an experiment in building a Linux distribution that uses Kubernetes
as its service manager. **Li**nux + **K**ubernetes. The name is the
architecture: a reconciler that continuously *likens* the machine to a
desired state declared in git.

## The idea

The OS image is just a bootloader for a git repo.

The immutable image contains only a kernel, a tiny init stub, and
[k3s](https://k3s.io). Everything else (system services, user apps, node
configuration) is a [Flux](https://fluxcd.io) Kustomization reconciled from
a git repository. Machine identity reduces to "which repo and path do I
reconcile."

Some things fall out of that naturally:

* **Backup is `git log`.** If all configuration lives in git, there's nothing
  to back up except data volumes. `/etc` stops being something you snapshot.
* **Updates are commits.** Flux's image automation keeps apps current; OS and
  kernel upgrades ride through
  [system-upgrade-controller](https://github.com/rancher/system-upgrade-controller),
  so even a kernel bump is a git commit.
* **Images survive the internet.** k3s's embedded
  [Spegel](https://spegel.dev) registry mirror means nodes share images
  peer-to-peer, and re-pulls come from the LAN.
* **System and user apps are just directories.** The same repo layout that
  works for a homelab cluster works for the machine itself.

## Prior art

This idea isn't new, and the neighbors are worth knowing:

* [Talos Linux](https://www.talos.dev) — no systemd, no shell, no SSH; the
  machine is a gRPC API. The closest thing to this that you can run in
  production today.
* [k3OS](https://github.com/rancher/k3os) — Rancher's "the OS is just k3s"
  distro, now archived. Almost exactly this idea.
* [Kairos](https://kairos.io) — k3OS's spiritual successor; immutable
  meta-distro for edge Kubernetes.
* [LinuxKit](https://github.com/linuxkit/linuxkit) and
  [Bottlerocket](https://github.com/bottlerocket-os/bottlerocket) — minimal
  immutable hosts where everything interesting runs in containers.

What none of them quite are is *GitOps-native from first boot*, where the
git repo isn't a layer you add to the OS, it *is* the OS.

## Status

A spike. Nothing works yet; there may not even be code here yet. This is
first and foremost a learning project: the goal is to understand what an
init system actually does by building the smallest one that can stand up
Kubernetes, then letting Kubernetes do the rest. The rough path lives in
[TODO.md](TODO.md).

## License

MIT
