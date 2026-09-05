# Plans

This directory holds the driver's design documents. Each one is
numbered in sequence and keeps its number for life.

[`00-design.md`](00-design.md) is the design. The numbered plans build
it, in order. A plan states a problem, states the contracts that answer
it, and states how the work is proved. It leaves the shape of the code
to whoever builds it. Each plan starts at low fidelity, and it is
raised to full fidelity before anyone builds it.

A plan moves to [`completed/`](completed/) when it is built and drilled
in the lab. A plan that is set aside moves to [`rejected/`](rejected/)
with the reasons that decided it. A question the current work cannot
answer is written to [`open-problems/`](open-problems/); those
documents have no number, because nobody has decided yet what work
they become.

## Planned

* [04, Writeable volumes, unarmed](04-writeable-volumes-unarmed.md).
  Proposed. A static `PersistentVolume` and a `ReadWriteOncePod`
  claim, a work tree per volume, and a driver that watches and reports
  but commits nothing.
* [05, Armed volumes](05-armed-volumes.md). Proposed. The
  `VolumeAttributesClass`, the controller plugin that accepts it, and
  commit, push, and the metadata ref.
* [06, Divergence and restore](06-divergence-and-restore.md).
  Proposed. The side branch, the rebase at stage, the restore drill,
  and the sweep of work trees nothing stages any more.

## Designs

* [01, The skeleton](completed/01-the-skeleton.md). Built, and drilled
  in the lab on 2026-09-05. The repository, its gates and workflows,
  its site, and a node plugin that registers with the kubelet and
  serves no volumes.
* [02, The lab](completed/02-the-lab.md). Built and run on 2026-09-05.
  One `liken` machine in QEMU from the public release channel, a git
  daemon on the host as its forge, and a smoke target that runs the
  chain in 78 seconds.
* [03, Read-only volumes](completed/03-read-only-volumes.md). Built,
  and drilled in the lab on 2026-09-05. Inline volumes that follow a
  ref from one bare repository per URL, with `pull`, `depth`, and
  `offline`, replaced file by file under the mount.

## Open problems

* [The volume condition moved in CSI 1.13](open-problems/the-volume-condition-moved-in-csi-1-13.md).
  Spec 1.13 replaced `VolumeCondition` with an alpha RPC the kubelet
  does not call yet, so the driver pins spec 1.12.
* [The driver forgets its volumes when it restarts](open-problems/the-driver-forgets-its-volumes-when-it-restarts.md).
  Published volumes live in memory, so a restarted driver follows
  nothing until the kubelet publishes again.
