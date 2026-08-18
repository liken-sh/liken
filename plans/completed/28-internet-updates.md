# Internet updates

Milestone 28. Completed. After a machine boots from install media,
every update comes from liken's public release channel, with no build
step for a cluster or a machine.

Milestone 22 split the OS into two archives: a generic liken.cpio that
is the same for everyone, and a small deployment layer that carries one
cluster's identity and manifests. The upgrade path still served composed
bytes. A machine's release catalog names the digest of the release
document that it will boot, and that document names the artifacts. Those
artifacts held the deployment's identity, so no digest was stable until
the deployment's own bytes were stable. Every deployment therefore paid
a cost for every release: download the new generic release, compose it
with the layer, publish the result to a channel, and run a web server
that the fleet can reach. That replaced compilation with composition,
but it was still a step that nobody should have to take.

This milestone meets one requirement. After a machine boots from install
media, every update after that comes from the internet, from liken's
public releases, with no build step for a cluster or a machine.

## The design

The two archives are no longer composed at publish time. They stay as
two separate files, all the way to the boot slot, and the firmware joins
them at load time:

* **A slot holds the OS and the layer side by side.** It holds vmlinuz,
  the generic liken.cpio, and the liken CLI, which are the artifacts
  that a public release document lists. It also holds deployment.cpio
  (the layer), a deployment.cpio.sha256 sidecar that names the layer's
  digest in `sha256sum -c` form, and the public release.yaml, stored
  byte for byte.
* **A boot entry has two initrd= parameters.** The kernel's EFI stub
  loads every initrd= occurrence in order and gives the kernel one
  concatenated image. The composed build used the same mechanism at
  build time, and this design uses it at load time. The layer's entries
  come second, so its files override the generic archive's files, as
  before.
* **Machines fetch public releases.** The Cluster's
  spec.releases.source points at liken's public channel, and the catalog
  pins public release.yaml digests. These digests are the same for
  everyone, so a release page can publish them or a GitOps repo can hold
  them. An upgrade becomes an edit to the Cluster document: add the
  catalog entry, and set spec.version. The fetcher downloads and
  verifies the public artifacts into the inactive slot, as it does
  today.
* **The machine carries its own layer forward.** The layer never travels
  over the network, because the machine already has it. The fetcher
  verifies the active slot's layer against its sidecar and copies both
  to the inactive slot, durably, before the release document lands. A
  slot is bootable, or it has no release.yaml at all.
* **One channel format.** The release server takes the place of the
  public releases on the liken.sh website, and it serves only public
  bundles.
  Nothing that is specific to a deployment is hosted. A person produces
  install media locally, from a downloaded release plus a deployment
  directory. The deployment's choices stay on each machine, on its slots
  and in its cluster's API, and never on a server.

The trust chain keeps its shape and is stronger in practice. The API
names the document, the document names the artifacts, and the digests
are now the digests that liken publishes. A deployment can therefore
verify what it is about to run against what the rest of the world
received. The layer is never downloaded, so the document needs no entry
for it. A machine checks the layer's integrity locally, against the
sidecar that the installer writes and that every carry checks.

## The two-initrd drill

The design depends on the EFI stub honoring more than one initrd=
parameter. Upstream documents that behavior but has deprecated it. The
first step of the milestone therefore proved the behavior under OVMF,
before anything was built on top of it.

The control boot came first. A machine was installed with the future
slot layout, with the generic liken.cpio and deployment.cpio as separate
files on the slot, but its boot entry named only the generic archive.
The kernel freed 130,524K of initrd, which is the generic archive alone.
The machine still reached the Ready state, because the install boot had
already put the manifests and the identity onto durable state. A settled
machine needs little from its layer at boot. The layer carries the seeds
that a first boot needs, plus the declared kernel modules, which are
only in the initramfs. This control run established the size measurement
for the real test.

The same machine was then reinstalled, with both parameters:

    initrd=\liken.cpio initrd=\deployment.cpio

The kernel freed 131,928K, and the extra 1,404K matches the 1,440,472
byte layer. The kernel unpacked the concatenation without a problem, and
the node reached the Ready state in less than one minute. OVMF's stub
(kernel 7.1.2) loads both files, in order, from the slot's filesystem.
The milestone kept a fallback plan in reserve, to compose the two
archives into a slot-local file after verification and to use one
initrd= parameter. The fallback was not needed.

## How it landed

1. **The drill.** The result above gated all the work that followed.
2. **`liken media` and the two-initrd installer.** Install-image
   assembly moved from image/install.sh into the CLI, in image/media.go,
   behind `liken media`. It verifies a release directory against its
   document, composes it with a layer, and writes install media. The
   media's payload carries the document byte for byte, and the layer
   beside its sidecar. machine/layer.go holds the vocabulary for this.
   The installer copies the layer to slot A with the same
   verify-copy-reverify discipline that the artifacts already use, it
   writes the sidecar last, and it writes both slots' two-initrd boot
   entries. Two tests proved this: a fresh install reached Ready from
   disk, and an install that was hard-killed mid-kernel, and again
   mid-copy, converged correctly on the next run.
3. **The fetcher carries the layer**, in machine-operator/fetch.go's
   carryLayer, between the artifact downloads and the document. If an
   active slot's layer or sidecar fails to verify, the fetch holds, the
   same way corruption already holds it, because no retry can repair the
   slot that the machine runs from. This case reports its own message,
   because the remedy is local: repair or reinstall this machine. A
   republish cannot fix it. A release round run before the channel
   reshape proved this: the composed image plus the carried layer still
   boots, because the layer unpacks twice. The two pieces of work could
   therefore land separately.
4. **One channel format.** `liken publish` and image/install.sh are
   gone, and dev-cluster/releases/ went with them. releases/dist is now
   the channel: `make release` bundles into it, `make serve` serves it,
   and the bundle's report ends with the catalog entry that a deployment
   commits, to adopt the release.

## The fleet drill

The milestone's proof was a three-leader fleet round on the one channel.
Three machines were wiped, installed from `liken media` output, and
reached Ready. Then `make release VERSION=0.3.0` and one Cluster edit,
spec.version plus the printed catalog entry, rolled the fleet one leader
at a time, onto bytes fetched straight from the public-format channel.
The serve log shows each machine pulling release.yaml, vmlinuz, the
generic liken.cpio, and the CLI. Nothing was composed or published for
the deployment at any point.

The corruption drill held. A release that was damaged after publish
(`make corrupt`) left every machine in the DigestMismatch state, with
nothing staged, and a retarget to a good version cleared the hold. The
layer's own failure mode was drilled as one sequence on one machine: a
hard kill mid-fetch, then the active slot's sidecar was truncated on
disk with qemu-nbd while the machine was down. The next boot resumed the
download, because the artifacts verified in place and nothing was
refetched. The boot then refused the carry with the local remedy: "the
running slot's layer sidecar is damaged (the layer sidecar is 0 bytes,
want 82); repair or reinstall this machine". A reinstall from fresh
media brought the machine back to the fleet's version, with its layer
restored.

The reinstall showed one manual step that this milestone leaves undone.
etcd refuses a wiped leader that rejoins under its old name, with
"duplicate node name found", until a person deletes the stale node
object. k3s then treats that deletion as the old member's removal.
Automatic cleanup, for a machine that is replaced on purpose, is
machine-lifecycle work for a later milestone.

## Decisions on record

* **No back-compatibility.** Machines that were installed under the
  composed layout are reinstalled, not migrated. Wiping a lab guest's
  directory removes its disks and firmware variables together. The
  project is pre-release, and the composed layout never shipped.
* **The CLI travels on the slot.** The public document lists it, and "a
  slot holds exactly what its document lists" is a simpler rule than a
  machine-side exception. Five megabytes buys recovery tools on every
  disk.
* **Layer updates over the network are out of scope.** The on-slot layer
  is a first-boot seed. Manifest changes already reach settled machines
  through the cluster's API, and a future mechanism can distribute
  refreshed layers, for new declared modules and new machines' seeds,
  the same way. Until then, a changed layer needs new media.
