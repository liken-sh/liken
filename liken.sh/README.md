# The liken.sh deployment

This directory is the project's own live deployment of liken. It
holds three things, and each has a different lifetime:

* **The release channel**: `https://releases.liken.sh`, where CI
  publishes every release of liken. The releases domain explains
  what a release is. `.github/workflows/release.yaml` is the
  publisher.
* **The cluster**: one Linode machine. It was installed once from a
  published release. Since then, it has upgraded itself from the
  channel through Cluster edits. Its declarations are `cluster.yaml`
  and `machines/`.
* **The website**: `https://liken.sh`, one static page served by the
  cluster. `website/` holds the page, its manifests, and the deploy
  documentation. A change to the page needs only a push, not an
  image rebuild or a reboot.

Everything the deployment needs lives here, organized the way any
liken deployment is:

* `terraform.tf` — the live infrastructure: the DNS zone, the release
  channel's bucket, and the credentials that CI uses to publish
  releases and renew the channel's certificate.
* `cluster.yaml` and `machines/` — the fleet, in liken's own
  vocabulary.
* `Makefile` — how this deployment builds the OS for its machine, and
  how it deploys the website to its cluster.
* `website/` — the site that the cluster serves: the page, its
  Kubernetes manifests, and the deploy documentation.
* `identity/` — the cluster's minted certificate authorities and
  join token, created by `make identity` and never committed.

## The release channel

The channel is a Linode Object Storage bucket, served over HTTPS at
its own name. The channel is deliberately not the cluster. Machines
upgrade themselves from the channel, so the channel must outlive any
machine that it feeds. If a cluster served its own updates, a failure
could leave nothing to rescue it.

The layout is exactly what liken's fetcher expects, and it is easy
to browse by hand:

    https://releases.liken.sh/channel.yaml
    https://releases.liken.sh/<version>/release.yaml
    https://releases.liken.sh/<version>/<artifact>

`channel.yaml` is the channel's one mutable object. liken's releases
are linear, so the root document only names the newest version, and
clusters poll it to learn when a newer version exists. This document
is advisory only. *Adopting* a release still means a Cluster edit
that names the version and pins the release document's digest, so
trust travels through the Cluster, never through the channel. Beyond
that one pointer, nothing lists the contents of the bucket. The
publishing workflow's run summary prints the digest for each release.

Two Linode details are worth recording here, so nobody has to
rediscover them. First, the bucket is *named* `releases.liken.sh`,
because that is how Linode's custom-domain TLS finds a bucket: the
name CNAMEs to the bucket's own hostname. Second, Linode has no ACME
service of its own. Because of this, a scheduled workflow
(`.github/workflows/releases-cert.yaml`) mints a fresh Let's Encrypt
certificate every month, by DNS-01 against the zone declared here,
and uploads it to the bucket.

## The machine, and why it boots the way it does

The cluster is one Linode nanode: 1 GB of memory, the smallest
machine that Linode sells. This is a deliberate constraint, not a
cost-saving choice. liken claims to be a lightweight OS, and that
claim is honest only if the project's own production cluster runs
comfortably inside 1 GB.

Linode shaped this deployment in a second way. **Akamai's hosts boot
guests only in BIOS style; there is no UEFI option at all.** On a
UEFI machine, liken boots with no bootloader of its own. The
firmware's boot entries load the kernel's EFI stub directly, and
upgrades actuate by changing firmware variables: BootNext tries a new
slot once, and BootOrder keeps that choice. None of that machinery
exists under BIOS. Because of this, this machine's manifest declares
the two storage roles that its other boot path needs: `biosBoot`, a
raw partition that holds GRUB's core image, and `bootHome`, a small
filesystem that holds GRUB's config and its environment block. The
installer writes all of this, including the MBR's boot code. Upgrades
actuate through the environment block in the same way they actuate
through firmware variables: `try_slot` is the one-shot trial, and
`default_slot` is the standing preference. The machine writes both
fields itself. Linode's "direct disk" boot setting only runs what the
MBR carries. Linode's own GRUB 2 loader does not work here: it reads
its configuration from the disk treated as one whole-disk filesystem,
the way Linode's own images are laid out, and it never looks inside a
partition table.

The project considered moving to a cloud with UEFI guests, and
decided to stay on Linode. BIOS-only machines are not a quirk
specific to Linode. They are a standing fact about the hardware that
liken will meet: old servers, cheap virtual machines, and other
clouds' legacy tiers. An OS that can only upgrade itself where UEFI
firmware exists would be narrower than liken means to be. This
deployment forced the project to build the missing capability:
[milestone 30](../plans/30-bios-upgrades.md) taught liken to actuate
upgrades by rewriting what GRUB reads, instead of by changing
firmware variables.

Linode's machinery adds two more details worth naming. First, the
boot code. Linode's image deploy once zeroed the MBR's 440 bytes, and
a failed background event could zero them again under a running
machine. This hazard is part of why the machine defends its own boot
path. On every boot, and before every reboot, the machine re-derives
the MBR's boot code, GRUB's core image, and the config from the
proven slot's artifacts, and it rewrites whatever disagrees. Today's
deploys measure byte-faithful end to end, so this healing is a
backstop, not a routine repair. It stays in place because the hazard
was real once, and it costs nothing to guard against. Second,
reboots. Linode turns a guest-initiated reboot into a power-off.
Because of this, every reboot that the machine performs on itself,
whether to try a new release or to roll back from one, ends with the
instance off. The shutdown watchdog (Lassie, enabled in terraform)
notices within a couple of minutes and boots the instance again.
Without the watchdog, the machine's first self-reboot would be its
last.

`make RELEASE=<version>` here builds the machine's install media from
a published release. The release is downloaded from the channel and
digest-verified, then actually installed in a local QEMU guest under
SeaBIOS, against a raw disk file, with no root privileges needed
anywhere. The storage design that this installs splits the machine's
disks by lifetime: a small disposable system disk (boot slots,
machine state, scratch) and a data disk. The data disk is claimed
once, holds everything durable, and is never reinstalled. The disk
image ships once, to found the machine. From then on, the machine
updates itself from the release channel through Cluster edits, and
the disk-writing path becomes disaster recovery only. Reinstalling
the OS costs a reboot, not the cluster.

## Reaching the cluster

The API server's certificate names the node's cluster-segment
address: the VLAN, where peers and future nodes live. Because of
this, reaching the API server over the public internet means telling
kubectl which name to expect. Every kubectl command in this
deployment carries the same pair of flags:

    kubectl --kubeconfig identity/kubeconfig \
      --server=https://<node public address>:6443 \
      --tls-server-name=10.10.0.1 \
      get nodes

The `kubectl` and `stern` wrappers in this directory carry that pair
of flags for you. `./liken.sh/kubectl get machines`, run from the
repository root, is the short way to ask the cluster anything.
