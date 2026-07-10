# Internet updates

Milestone 28 — Done

Milestone 22 split the OS into two archives: a generic liken.cpio that
is the same for everyone, and a small deployment layer carrying one
cluster's identity and manifests. But the upgrade path still serves
composed bytes. A machine's release catalog names the digest of the
release document it will boot, that document names the artifacts, and
because those artifacts embed the deployment's identity, no digest is
stable until the deployment's own bytes are. The consequence is a
tax on every deployment for every release: download the new generic
release, compose it with the layer, publish the result to a channel,
and run a web server the fleet can reach. Composition instead of
compilation, but still a step nobody should have to take.

The requirement this milestone exists to meet: once a machine has
booted from install media, every update after that comes from the
internet — liken's public releases — with no per-cluster or
per-machine build step of any kind.

## The design

Stop composing at publish time. The two archives stay two files all
the way to the boot slot, and the firmware joins them at load time:

* **A slot holds the OS and the layer side by side.** vmlinuz, the
  generic liken.cpio, and the liken CLI — exactly the artifacts a
  public release document lists — plus deployment.cpio (the layer),
  a deployment.cpio.sha256 sidecar naming the layer's digest in
  `sha256sum -c` form, and the public release.yaml, byte for byte.
* **Boot entries carry two initrd= parameters.** The kernel's EFI
  stub loads every initrd= occurrence in order and hands the kernel
  one concatenated image — the same mechanism the composed build
  relied on, moved from build time to load time. The layer's entries
  come second, so its files override the generic archive's, exactly
  as before.
* **Machines fetch public releases.** The Cluster's
  spec.releases.source points at liken's public channel, and the
  catalog pins public release.yaml digests — stable for everyone,
  publishable on a release page, committable to a GitOps repo. An
  upgrade is an edit to the Cluster document: add the catalog entry,
  set spec.version. The fetcher downloads and verifies the public
  artifacts into the inactive slot exactly as it does today.
* **The machine carries its own layer forward.** The layer never
  travels over the network, because the machine already has it: the
  fetcher verifies the active slot's layer against its sidecar and
  copies both to the inactive slot, durably, before the release
  document lands. A slot is bootable or it has no release.yaml.
* **One channel format.** The release server is a stand-in for the
  public releases on the liken.sh website, and it serves only public
  bundles. Nothing deployment-specific is ever hosted: install media
  is produced locally (a downloaded release plus a deployment
  directory), and the deployment's choices live on each machine —
  on its slots and in its cluster's API — not on a server.

The trust chain is unchanged in shape and stronger in practice: the
API names the document, the document names the artifacts, and the
digests are now the same ones liken publishes, so a deployment can
verify what it is about to run against what the world got. The layer
is never downloaded, so it needs no entry in the document; its
integrity is local, the sidecar written at install time and checked
at every carry.

## What the drill showed

The design leans on the EFI stub honoring more than one initrd=
parameter, a behavior that is documented but deprecated upstream, so
the first act of the milestone was to prove it under OVMF before
building anything on it.

The control boot came first: a machine installed with the future slot
layout (generic liken.cpio and deployment.cpio as separate files on
the slot) but a boot entry naming only the generic archive. The
kernel freed 130,524K of initrd — the generic archive alone — and,
notably, the machine still reached Ready: the install boot had
already seeded the manifests and identity onto durable state, so a
settled machine barely needs its layer at boot. What the layer
carries per-boot is the seeds a first boot needs and the declared
kernel modules, which live only in the initramfs; the control run
established the size measurement that makes the real test
discriminating.

Then the same machine, reinstalled with both parameters:

    initrd=\liken.cpio initrd=\deployment.cpio

The kernel freed 131,928K — the extra 1,404K matching the 1,440,472
byte layer — unpacked the concatenation without complaint, and the
node was Ready in under a minute. OVMF's stub (kernel 7.1.2) loads
both files, in order, from the slot's filesystem. The fallback this
milestone kept in reserve (composing the two archives into a
slot-local file after verification, one initrd=) is not needed.

## How it landed

1. **The drill** — the verdict above gated everything else.
2. **`liken media` and the two-initrd installer.** Install-image
   assembly moved from image/install.sh into the CLI (image/media.go
   behind `liken media`): verify a release directory against its
   document, compose it with a layer, and write install media whose
   payload carries the document verbatim and the layer beside its
   sidecar (the vocabulary lives in machine/layer.go). The installer
   copies the layer to slot A with the artifacts' own
   verify-copy-reverify discipline, sidecar last, and writes both
   slots' two-initrd boot entries. Proven: fresh install to Ready
   from disk; an install hard-killed mid-kernel and again mid-copy
   converged on the next run.
3. **The fetcher carries the layer** (machine-operator/fetch.go's
   carryLayer), between the artifact downloads and the document. An
   active slot whose layer or sidecar doesn't verify holds the fetch
   the way corruption does — no retry can repair the slot the
   machine is standing on — but under its own message, because the
   remedy (repair or reinstall this machine) is local, not a
   republish. Proven with a release round before the channel
   reshape: the composed image plus the carried layer boots (the
   layer just unpacks twice), so the two slices could land apart.
4. **One channel format.** `liken publish` and image/install.sh are
   gone, dev-cluster/releases/ with them; releases/dist is the
   channel, `make release` bundles into it, `make serve` serves it,
   and the bundle's report ends with the catalog entry a deployment
   commits to adopt the release.

## What the lab showed

The milestone's proof was a three-leader fleet round on the one
channel. Three machines wiped, installed from `liken media` output,
and Ready; `make release VERSION=0.3.0`, one Cluster edit
(spec.version plus the printed catalog entry), and the fleet rolled
one leader at a time onto bytes fetched straight from the
public-format channel — the serve log shows each machine pulling
release.yaml, vmlinuz, the generic liken.cpio, and the CLI, and
nothing was composed or published for the deployment at any point.

The corruption drill held: a release damaged after publish
(`make corrupt`) left every machine at DigestMismatch with nothing
staged, and retargeting a good version cleared the hold. The layer's
own failure mode was drilled as one sequence on one machine: a hard
kill mid-fetch, then its active slot's sidecar truncated on disk
while it was down (qemu-nbd). The next boot resumed the download —
artifacts verified in place, nothing refetched — and then refused
the carry with the local remedy: "the running slot's layer sidecar
is damaged (the layer sidecar is 0 bytes, want 82); repair or
reinstall this machine". A reinstall from fresh media converged it
back to the fleet's version, its layer restored. The reinstall
surfaced one manual step this milestone leaves on the table: a wiped
leader rejoining under its old name is refused by etcd ("duplicate
node name found") until the stale node object is deleted, which k3s
turns into the old member's removal. Automating that cleanup when a
machine is deliberately replaced is machine-lifecycle work for a
later milestone.

## Decisions on record

* **No back-compatibility.** Machines installed under the composed
  layout are reinstalled, not migrated; wiping a lab guest's
  directory removes its disks and firmware variables together. The
  project is pre-release and the composed layout never shipped.
* **The CLI rides the slot.** The public document lists it, and "a
  slot carries exactly what its document lists" is a simpler rule
  than a machine-side exception; five megabytes buys recovery
  tooling on every disk.
* **Layer updates over the network are out of scope.** The on-slot
  layer is a first-boot seed. Manifest changes already reach settled
  machines through the cluster's API, and a future mechanism can
  distribute refreshed layers (new declared modules, new machines'
  seeds) the same way. Until then, a changed layer means new media.
