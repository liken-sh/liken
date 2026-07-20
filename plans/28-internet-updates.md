# Internet updates

Milestone 28 — Done

Milestone 22 split the OS into two archives: a generic liken.cpio that
is the same for everyone, and a small deployment layer that carries
one cluster's identity and manifests. But the upgrade path still
served composed bytes. A machine's release catalog names the digest of
the release document it will boot, that document names the artifacts,
and because those artifacts embed the deployment's identity, no digest
stays stable until the deployment's own bytes do. The result was a tax
on every deployment for every release: download the new generic
release, compose it with the layer, publish the result to a channel,
and run a web server that the fleet can reach. This replaced
compilation with composition, but it was still a step that nobody
should have to take.

This milestone exists to meet one requirement: once a machine has
booted from install media, every update after that must come from the
internet, from liken's public releases, with no build step for any
particular cluster or machine.

## The design

The fix is to stop composing bytes at publish time. The two archives
stay as two separate files, all the way to the boot slot, and the
firmware joins them at load time instead:

* **A slot holds the OS and the layer side by side.** It carries
  vmlinuz, the generic liken.cpio, and the liken CLI (exactly the
  artifacts that a public release document lists), plus
  deployment.cpio (the layer), a deployment.cpio.sha256 sidecar naming
  the layer's digest in `sha256sum -c` form, and the public
  release.yaml, stored byte for byte.
* **Boot entries carry two initrd= parameters.** The kernel's EFI stub
  loads every initrd= occurrence in order and hands the kernel one
  concatenated image. This is the same mechanism that the composed
  build relied on, moved from build time to load time. The layer's
  entries come second, so its files override the generic archive's
  files, exactly as before.
* **Machines fetch public releases.** The Cluster's
  spec.releases.source points at liken's public channel, and the
  catalog pins public release.yaml digests. These digests stay stable
  for everyone, so they can be published on a release page or
  committed to a GitOps repo. An upgrade becomes an edit to the
  Cluster document: add the catalog entry, and set spec.version. The
  fetcher downloads and verifies the public artifacts into the
  inactive slot exactly as it does today.
* **The machine carries its own layer forward.** The layer never
  travels over the network, because the machine already has it: the
  fetcher verifies the active slot's layer against its sidecar and
  copies both to the inactive slot, durably, before the release
  document lands. A slot is either bootable, or it has no
  release.yaml at all.
* **One channel format.** The release server takes the place of the
  public releases on the liken.sh website, and it serves only public
  bundles. Nothing deployment-specific is ever hosted: install media
  is produced locally, from a downloaded release plus a deployment
  directory, and the deployment's choices live only on each machine,
  on its slots and in its cluster's API, never on a server.

The trust chain keeps the same shape and grows stronger in practice.
The API names the document, the document names the artifacts, and the
digests are now the same ones that liken publishes, so a deployment
can verify what it is about to run against what the rest of the world
received. The layer is never downloaded, so it needs no entry in the
document. Its integrity is checked locally, through the sidecar
written at install time and checked at every carry.

## What the drill showed

The design depends on the EFI stub honoring more than one initrd=
parameter, a behavior that upstream documents but has deprecated. So
the first step of the milestone was to prove this behavior under OVMF
before building anything on top of it.

The control boot came first. A machine was installed with the future
slot layout (generic liken.cpio and deployment.cpio as separate files
on the slot), but its boot entry named only the generic archive. The
kernel freed 130,524K of initrd, the generic archive alone, and,
notably, the machine still reached the Ready state: the install boot
had already seeded the manifests and identity onto durable state, so a
settled machine barely needs its layer at boot. What the layer carries
on each boot is the seeds that a first boot needs, plus the declared
kernel modules, which live only in the initramfs. This control run
established the size measurement that made the real test meaningful.

The team then reinstalled the same machine, with both parameters:

    initrd=\liken.cpio initrd=\deployment.cpio

The kernel freed 131,928K, and the extra 1,404K matched the 1,440,472
byte layer. It unpacked the concatenation without a problem, and the
node reached the Ready state in under a minute. OVMF's stub (kernel
7.1.2) loads both files, in order, from the slot's filesystem. The
fallback plan that this milestone had kept in reserve, composing the
two archives into a slot-local file after verification and using one
initrd= parameter, turned out not to be needed.

## How it landed

1. **The drill.** The verdict above gated everything else that
   followed.
2. **`liken media` and the two-initrd installer.** Install-image
   assembly moved from image/install.sh into the CLI (image/media.go,
   behind `liken media`). It verifies a release directory against its
   document, composes it with a layer, and writes install media whose
   payload carries the document verbatim and the layer beside its
   sidecar (the vocabulary for this lives in machine/layer.go). The
   installer copies the layer to slot A with the same
   verify-copy-reverify discipline that the artifacts already use,
   writing the sidecar last, and writes both slots' two-initrd boot
   entries. This was proven two ways: a fresh install reached Ready
   from disk, and an install hard-killed mid-kernel, and again
   mid-copy, converged correctly on the next run.
3. **The fetcher carries the layer** (machine-operator/fetch.go's
   carryLayer), between the artifact downloads and the document. If an
   active slot's layer or sidecar fails to verify, the fetch holds,
   the same way corruption already holds it, because no retry can
   repair the slot that the machine is standing on. But this case
   reports its own message, because the remedy (repair or reinstall
   this machine) is local, not something a republish can fix. This was
   proven with a release round run before the channel reshape: the
   composed image plus the carried layer still boots, because the
   layer simply unpacks twice, so the two pieces of work could land
   separately.
4. **One channel format.** `liken publish` and image/install.sh are
   gone, and dev-cluster/releases/ went with them. releases/dist is
   now the channel: `make release` bundles into it, `make serve`
   serves it, and the bundle's report ends with the catalog entry that
   a deployment commits, to adopt the release.

## What the lab showed

The milestone's proof was a three-leader fleet round on the one
channel. Three machines were wiped, installed from `liken media`
output, and reached Ready. Then `make release VERSION=0.3.0` and one
Cluster edit (spec.version plus the printed catalog entry) rolled the
fleet, one leader at a time, onto bytes fetched straight from the
public-format channel. The serve log shows each machine pulling
release.yaml, vmlinuz, the generic liken.cpio, and the CLI, and
nothing was composed or published for the deployment at any point.

The corruption drill held. A release damaged after publish (`make
corrupt`) left every machine in the DigestMismatch state, with nothing
staged, and retargeting a good version cleared the hold. The layer's
own failure mode was drilled as one sequence on one machine: a hard
kill mid-fetch, then the active slot's sidecar was truncated on disk
while the machine was down (using qemu-nbd). The next boot resumed the
download (the artifacts verified in place, and nothing was refetched)
and then refused the carry with the local remedy: "the running slot's
layer sidecar is damaged (the layer sidecar is 0 bytes, want 82);
repair or reinstall this machine". A reinstall from fresh media
converged the machine back to the fleet's version, with its layer
restored. The reinstall surfaced one manual step that this milestone
leaves undone: a wiped leader rejoining under its old name is refused
by etcd ("duplicate node name found"), until someone deletes the stale
node object, which k3s then treats as the old member's removal.
Automating that cleanup, for when a machine is deliberately replaced,
is machine-lifecycle work for a later milestone.

## Decisions on record

* **No back-compatibility.** Machines installed under the composed
  layout are reinstalled, not migrated. Wiping a lab guest's directory
  removes its disks and firmware variables together. The project is
  pre-release, and the composed layout never shipped.
* **The CLI travels on the slot.** The public document lists it, and "a
  slot carries exactly what its document lists" is a simpler rule than
  a machine-side exception. Five megabytes buys recovery tooling on
  every disk.
* **Layer updates over the network are out of scope.** The on-slot
  layer is a first-boot seed. Manifest changes already reach settled
  machines through the cluster's API, and a future mechanism can
  distribute refreshed layers (new declared modules, new machines'
  seeds) the same way. Until then, a changed layer means new media.
