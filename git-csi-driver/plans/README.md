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

Nothing is planned. The next work comes out of
[`open-problems/`](open-problems/).

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
* [04, Writeable volumes, unarmed](completed/04-writeable-volumes-unarmed.md).
  Built, and drilled in the lab on 2026-09-05. A static
  `PersistentVolume` and a `ReadWriteOncePod` claim, a work tree per
  volume, a driver that watches and reports but commits nothing, and
  the record that survives a restart.
* [05, Armed volumes](completed/05-armed-volumes.md). Built, and
  drilled in the lab on 2026-09-05. The `VolumeAttributesClass`, the
  controller plugin that validates it, and commit, push, the metadata
  ref, and restore.
* [06, Divergence and restore](completed/06-divergence-and-restore.md).
  Built, and drilled in the lab on 2026-09-05. The reconcile at stage,
  the side branch and its heal, the restore on a fresh node, and the
  sweep of work trees nothing stages.
* [07, Read-only claims](completed/07-read-only-claims.md). Built, and
  drilled in the lab on 2026-09-06. A `ReadOnlyMany` `PersistentVolume`
  and its claim, one staged tree per handle that every pod on the node
  publishes read-only, and the `Event`s and the gauge on the claim.
* [08, The store stays bounded](completed/08-the-store-stays-bounded.md).
  Built, and drilled in the lab on 2026-09-06. At every sweep the
  driver deletes the refs under `refs/git-csi/` that no volume follows
  and runs `git gc` in each bare repository that stays.
* [09, Rebase before push](completed/09-rebase-before-push.md). Built,
  and drilled in the lab on 2026-09-06. A rejected push rebases in a
  scratch tree and moves the pod's tree with `read-tree`, a diverged
  volume heals at its next push, and the metadata record follows the
  same rule, so many writers share one repository through `subPath`.
* [12, Webhooks demand a pull](completed/12-webhooks-demand-a-pull.md).
  Built, and drilled in the lab on 2026-09-06. The controller accepts a
  forge's push webhook, verified against a `Secret` the claim's
  namespace owns through the `webhookSecret` attribute, and marks the
  matching `PersistentVolume`s. `controller` and `node` are
  subcommands.
* [10, Pull on demand](completed/10-pull-on-demand.md). Built, and
  drilled in the lab on 2026-09-06. An annotation on a
  `PersistentVolume` demands a pull, `pull: on-demand` names a volume
  that pulls only then, and a restart pulls once.
* [11, A lean image](completed/11-a-lean-image.md). Built, and
  drilled in the lab on 2026-09-06. The image is a closure on scratch
  with a stripped binary, 79.9 MB where the release before was 263 MB,
  and the release gate runs git in it before a push.

## Open problems

Each one is a question the current work does not answer, written down
so the next plan can start from the facts.

* [Credentials after a restart](open-problems/credentials-after-a-restart.md).
  A writeable volume resumes with no credential, so every push fails
  until its pod restarts.
* [The store on the wrong filesystem](open-problems/the-store-on-the-wrong-filesystem.md).
  A node without the pod-storage partition puts the store on the RAM
  overlay, and nothing says so until the reboot.
* [Monitoring](open-problems/monitoring.md). The gauges exist, and
  nothing in `deploy/` scrapes them or alerts on them.
* [What git the driver does not serve](open-problems/what-git-the-driver-does-not-serve.md).
  Submodules, LFS, and a shallow writeable volume.

## Rejected

* [The volume condition](rejected/the-volume-condition.md). CSI spec
  1.13 removed it, the kubelet reads it only behind an alpha gate k3s
  leaves off, and no kubelet calls its replacement yet. The driver
  reports through events and the `git_csi_volume_abnormal` gauge.
