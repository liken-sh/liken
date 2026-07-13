# The liken.sh deployment

This directory is the project's public presence, and a complete, real
deployment of liken. Two different things live under the one name,
with deliberately different lifetimes:

* **The release channel, which is live**: `https://releases.liken.sh`,
  where CI publishes every release of liken (the releases domain
  explains what a release is; `.github/workflows/release.yaml` is the
  publisher).
* **The cluster, which is declared but not currently provisioned**:
  the machine that runs liken and serves the project's website. Its
  declarations (`cluster.yaml`, `machines/`) stay ready; the section
  below explains what it waits on.

Everything the deployment needs lives here, organized the way any
liken deployment would be:

* `terraform.tf` — the live infrastructure: the DNS zone, the release
  channel's bucket, and the credentials CI uses to publish releases
  and renew the channel's certificate.
* `cluster.yaml` and `machines/` — the fleet, in liken's own
  vocabulary.
* `Makefile` — how the OS gets built for this deployment's machine.
* `grub.cfg` — the boot entry, in GRUB's language (below explains
  why GRUB is here at all).
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

    https://releases.liken.sh/<version>/release.yaml
    https://releases.liken.sh/<version>/<artifact>

Nothing lists the bucket, by design. A Cluster document names the
version it wants and pins the release document's digest, so discovery
and trust both travel through the Cluster, never the channel. The
digest for each release is printed by the publishing workflow's run
summary.

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
guests BIOS-style only — there is no UEFI option at all.** liken
normally boots with no bootloader of its own: the firmware's boot
entries load the kernel's EFI stub directly, and upgrades actuate by
flipping firmware variables (BootNext to try a new slot once,
BootOrder to keep it). None of that machinery exists under BIOS. So
this machine's disk carries its own GRUB — first stage in the MBR,
the rest in a BIOS boot partition tucked into the GPT's alignment
gap, and its config and modules under `/boot/grub` on the active
slot — and Linode's "direct disk" boot setting simply executes what
the MBR carries. (Linode's own GRUB 2 loader is a dead end here: it
reads its config from the disk treated as one whole-disk filesystem,
the way Linode's own images are laid out, and never looks inside a
partition table.)

We considered moving to a cloud with UEFI guests and decided to stay.
BIOS-only machines are not a Linode quirk; they are a standing fact of
the hardware liken will meet — old servers, cheap virtual machines,
other clouds' legacy tiers. An OS that only knows how to upgrade
itself where UEFI firmware exists is narrower than liken means to be,
and this deployment forces the missing capability honestly: teaching
liken to actuate upgrades by rewriting GRUB's configuration instead of
firmware variables is [its own milestone](../plans/30-bios-upgrades.md).

That milestone is what the cluster waits on. Without it, every OS
update means re-shipping the whole system disk from a workstation,
and Linode's machinery fights that at each step: image deploys zero
the MBR's boot code (a rescue boot and a 440-byte repair every time),
a failed background event can zero it again up to an hour later, and
writing a raw disk needs their serial console to open a door first.
Operating a machine that can only be updated that way teaches the
wrong lesson, so the machine stays down until it can update itself:
install once from the release channel, then upgrade by Cluster edits,
with the disk-writing path demoted to disaster recovery. `make` here
still builds the machine's install media — a real liken install run
in a local QEMU guest against a raw disk file, GRUB planted on the
result — and the storage design it installs splits the machine's
disks by lifetime: a small disposable system disk (boot slots,
machine state, scratch) and a data disk that is claimed once, holds
everything durable, and is never reinstalled. Reinstalling the OS
costs a reboot, not the cluster.

## Reaching the cluster

The API server's certificate names the node's cluster-segment address
(the VLAN, where peers and future nodes live), so reaching it over
the public internet means telling kubectl which name to expect —
every kubectl in this deployment carries the same pair:

    kubectl --kubeconfig identity/kubeconfig \
      --server=https://<node public address>:6443 \
      --tls-server-name=10.10.0.1 \
      get nodes
