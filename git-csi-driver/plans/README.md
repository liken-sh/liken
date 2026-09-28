# Plans

This directory contains the driver's design documents. Each document is
numbered in sequence, and its number never changes.

[`00-design.md`](00-design.md) is the design. The numbered plans build
it, in order. Each plan states a problem, the contracts that address it,
and how the work is proved. It leaves the shape of the code to whoever
builds it. Each plan starts at low fidelity and reaches full fidelity
before implementation.

A plan moves to [`completed/`](completed/) when it is built. A plan that is set aside moves to [`rejected/`](rejected/)
with the reasons that decided it. A question the current work cannot
answer is written to `open-problems/`. Those documents have no number
because no work item exists for them yet.

A plan closes in the commit that builds it. That commit moves the
document to `completed/`, dates its header, and states what the lab
measured if a drill ran. A drill that has not run yet is not a reason
to leave a plan open. The built part closes, and the part still owed
becomes a new plan or an open problem.

## Planned

Nothing is planned. One open problem is written down.

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
* [13, Prometheus metrics](completed/13-prometheus-metrics.md). Built and drilled on liken-1 on 2026-09-10. The
  driver serves Prometheus metrics on port 9200 under `liken`'s shared
  contract: CSI operations as the reconcile layer, volumes mounted,
  fetch duration and failures per repository, and store size. This
  closes the open problem "Monitoring".
* [14, The watches use client-go](completed/14-the-watches-use-client-go.md).
  Built on 2026-09-27; the drill on liken-1 is still owed. The node
  plugin's two watches run on client-go's reflector through the typed
  clientset the driver already links, and the loop written by hand is
  gone. The controller's resizer sidecar elects a leader, so two
  replicas are safe.
* [15, Credentials return by republish](completed/15-credentials-return-by-republish.md).
  Built on 2026-09-28; the drill on liken-1 is still owed. The
  CSIDriver sets `requiresRepublish`, so the kubelet sends each
  volume's `nodePublishSecretRef` `Secret` again on every pod sync. A
  restarted driver takes its credentials back from those calls and
  fetches or pushes at once, and a repeated publish mounts nothing and
  runs no git. This closes the open problem "Credentials after a
  restart".

## Open problems

Each one is a question the current work does not answer, written down
so the next plan can start from the facts.

* [Every node watches every `PersistentVolume`](open-problems/every-node-watches-every-persistentvolume.md).
  No selector names the driver, so each node plugin lists, stores, and
  watches the `PersistentVolume`s of every driver in the cluster.

## Rejected

* [The volume condition](rejected/the-volume-condition.md). CSI spec
  1.13 removed it, the kubelet reads it only behind an alpha gate k3s
  leaves off, and no kubelet calls its replacement yet. The driver
  reports through events and the `git_csi_volume_abnormal` gauge.
* [The store on the wrong filesystem](rejected/the-store-on-the-wrong-filesystem.md).
  A store in memory is a valid choice: a read-only tree can be cloned
  again, and a writeable tree may last only as long as the node's
  uptime. Where the store lives is the cluster owner's choice.
