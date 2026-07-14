# The liken.sh deployment

This directory is the project's public presence, and a complete, real
deployment of liken. Two different things live under the one name,
with deliberately different lifetimes:

* **The release channel, which is live**: `https://releases.liken.sh`,
  where CI publishes every release of liken (the releases domain
  explains what a release is; `.github/workflows/release.yaml` is the
  publisher).
* **The cluster, which is live**: one Linode machine, installed once
  from a published release and upgrading itself from the channel by
  Cluster edits ever since. Its declarations are `cluster.yaml` and
  `machines/`; the website it will serve again is follow-on work.

Everything the deployment needs lives here, organized the way any
liken deployment would be:

* `terraform.tf` — the live infrastructure: the DNS zone, the release
  channel's bucket, and the credentials CI uses to publish releases
  and renew the channel's certificate.
* `cluster.yaml` and `machines/` — the fleet, in liken's own
  vocabulary.
* `Makefile` — how the OS gets built for this deployment's machine.
* `identity/` — the cluster's minted certificate authorities and
  join token, created by `make identity` and never committed.

## The release channel

The channel is a Linode Object Storage bucket, served over HTTPS at
its own name, and deliberately *not* the cluster: machines upgrade
themselves from the channel, so the channel has to outlive any
machine it feeds — a cluster serving its own updates could never be
rescued by one.

The layout is exactly what liken's fetcher and any curious person
expect:

    https://releases.liken.sh/channel.yaml
    https://releases.liken.sh/<version>/release.yaml
    https://releases.liken.sh/<version>/<artifact>

`channel.yaml` is the channel's one mutable object: liken's releases
are linear, so the root document just names the newest version, and
clusters poll it to learn that something newer exists. It is advisory
only — *adopting* a release still means a Cluster edit naming the
version and pinning the release document's digest, so trust travels
through the Cluster, never the channel. Beyond that pointer nothing
lists the bucket. The digest for each release is printed by the
publishing workflow's run summary.

Two Linode particulars, so nobody has to rediscover them: the bucket
is *named* `releases.liken.sh` because that is how Linode's
custom-domain TLS finds a bucket (the name CNAMEs to the bucket's own
hostname), and Linode has no ACME of its own, so a scheduled workflow
(`.github/workflows/releases-cert.yaml`) mints a fresh Let's Encrypt
certificate monthly, by DNS-01 against the zone declared here, and
uploads it to the bucket.

## The machine, and why it boots the way it does

The cluster is one Linode nanode: 1 GB of memory, the smallest
machine Linode sells. That is a deliberate constraint, not thrift —
liken claims to be a lightweight OS, and the claim is only honest if
the project's own production cluster runs comfortably inside 1 GB.

Linode shaped this deployment in a second way: **Akamai's hosts boot
guests BIOS-style only — there is no UEFI option at all.** On a UEFI
machine liken boots with no bootloader of its own: the firmware's
boot entries load the kernel's EFI stub directly, and upgrades
actuate by flipping firmware variables (BootNext to try a new slot
once, BootOrder to keep it). None of that machinery exists under
BIOS, so this machine's manifest declares the two storage roles that
ask for liken's other boot path: `biosBoot`, a raw partition holding
GRUB's core image, and `bootHome`, a small filesystem holding GRUB's
config and its environment block. The installer lays all of it down
— the MBR's boot code included — and upgrades actuate through the
environment block exactly the way they do through firmware variables:
`try_slot` is the one-shot trial, `default_slot` the standing
preference, both written by the machine itself. Linode's "direct
disk" boot setting simply executes what the MBR carries. (Linode's
own GRUB 2 loader is a dead end here: it reads its config from the
disk treated as one whole-disk filesystem, the way Linode's own
images are laid out, and never looks inside a partition table.)

We considered moving to a cloud with UEFI guests and decided to stay.
BIOS-only machines are not a Linode quirk; they are a standing fact of
the hardware liken will meet — old servers, cheap virtual machines,
other clouds' legacy tiers. An OS that only knows how to upgrade
itself where UEFI firmware exists is narrower than liken means to be,
and this deployment forced the missing capability honestly:
[milestone 30](../plans/30-bios-upgrades.md) taught liken to actuate
upgrades by rewriting what GRUB reads instead of firmware variables.

Linode's machinery adds two particulars worth naming. First, the
boot code: their image deploys once zeroed the MBR's 440 bytes (and
a failed background event could zero them again under a running
machine), which is part of why the machine defends its own boot
path — on every boot and before every reboot, it re-derives the
MBR's boot code, GRUB's core image, and the config from the proven
slot's artifacts and rewrites whatever disagrees. Today's deploys
measure byte-faithful end to end, so the healing is a backstop
rather than a routine repair; it stays because the hazard was real
once and costs nothing to guard against. Second, reboots: Linode
turns a guest-initiated reboot into a power-off, so every reboot the
machine performs on itself — trying a new release, rolling back from
one — ends with the instance off. The shutdown watchdog (Lassie,
enabled in terraform) notices within a couple of minutes and boots
it again; without the watchdog, the machine's first self-reboot
would be its last.

`make RELEASE=<version>` here builds the machine's install media
from a published release — downloaded from the channel and
digest-verified, then really installed in a local QEMU guest under
SeaBIOS, against a raw disk file, no root privileges anywhere — and
the storage design
it installs splits the machine's disks by lifetime: a small
disposable system disk (boot slots, machine state, scratch) and a
data disk that is claimed once, holds everything durable, and is
never reinstalled. The disk image ships once to found the machine;
from then on it updates itself from the release channel by Cluster
edits, with the disk-writing path demoted to disaster recovery.
Reinstalling the OS costs a reboot, not the cluster.

## Reaching the cluster

The API server's certificate names the node's cluster-segment address
(the VLAN, where peers and future nodes live), so reaching it over
the public internet means telling kubectl which name to expect —
every kubectl in this deployment carries the same pair:

    kubectl --kubeconfig identity/kubeconfig \
      --server=https://<node public address>:6443 \
      --tls-server-name=10.10.0.1 \
      get nodes

The `kubectl` and `stern` wrappers in this directory carry that pair
for you: `./liken.sh/kubectl get machines` from the repo root is the
short way to ask the cluster anything.
