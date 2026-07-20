# Declarative upgrades

Milestone 12 — Done

(Milestone 28 later changed two details in this document. Boot
slots now hold the generic OS and the deployment layer as separate
files. Boot entries now carry two initrd= parameters. Machines now
fetch releases from liken's public channel, not from a
per-deployment one. The machinery this milestone built —
spec.version, the catalog, the fetcher, proving boots, BootOrder
repair — did not change. plans/28-internet-updates.md has the
design.)

Declarative upgrades: one field on the Cluster moves the whole
fleet to a new liken version. `spec.version` names the target.

`spec.version` lives on the Cluster, not on the Machines. An
operator can retarget a fleet in one edit. Each machine reports
its own version in its status, where liken records real state,
not in its spec.

Machines that do not run the target version download the release.
Each machine writes the release into the boot slot it is not
running from. It then reboots into that slot through milestone
13's rollout. The rollout upgrades workers first, then leaders one
at a time, with no human supervising.

liken's OS has two files: vmlinuz and liken.cpio. An upgrade
replaces both files and reboots the machine. This two-file design
makes A/B slots and roll-back-on-failed-boot the natural choice.

This milestone also answers a question that QEMU's `-kernel` flag
had left open: liken has no bootloader. The kernel is already an
EFI executable, called the stub. UEFI firmware's own boot menu
picks a slot to boot. The firmware's BootNext variable holds the
"try the new slot once, fall back if it fails" logic. The firmware
handles fallback itself; no software is involved.

Trust for upgrades works the same way trust works everywhere else
in liken: through explicit inputs. The Cluster carries a release
source URL and a catalog. The catalog maps each version to the
digest of its release manifest. The release manifest carries the
sha256 of every artifact. The verification chain runs from the
API, to the catalog digest, to the manifest, to the artifact
bytes. liken adds no signatures until the hardening tier.

liken reads the target and the catalog live. Neither counts as
cluster-document drift. This follows the precedent set by
sysctls: a catalog append must not force a fleet-wide reboot.

Promotion follows the same pattern as the cluster document. Init
boots the staged slot tentatively, using an attempted marker and
BootNext. The operator's first reconcile on that slot is the proof
that the boot succeeded. Init re-asserts BootOrder from the
durable record on every boot after that. machineState is the
authority for what boots. The firmware only caches that authority.
1. [x] The slot vocabulary and the FAT32 formatter: `systemA` and
   `systemB` join the storage roles at the head of the canonical
   order. The firmware is the first reader of any partition liken
   owns, so these roles come first. The GPT types both as EFI
   System Partitions. They are the first roles whose partition
   type is not "Linux filesystem data", because firmware
   recognizes a candidate slot by its type GUID.

   Formatting uses a hand-written FAT32 formatter, built in the
   same style as the GPT writer. It writes a boot sector that
   describes the geometry, two copies of the allocation table,
   and a root directory that is just cluster 2. FAT is the only
   filesystem firmware promises to read, so liken formats slots
   as FAT32. The kernel's vfat module handles file I/O after
   that. This moves module loading ahead of storage settling in
   init, because some roles now depend on filesystem modules.

   Prove it: a node with a blank third disk claims both slots
   into status.storage, and a file written to a mounted slot
   survives a reboot.

   Proven on node-5, through a live Machine edit that rode the
   milestone-13 rollout. The machine reported AwaitingTurn, was
   granted a reboot, printed the claim and both FAT32 formats to
   the console, and reported both slots as Partition-backed at
   512Mi.

   The power-cut drill taught the milestone's next lesson early.
   An *unsynced* file written seconds before a power cut was gone
   afterward, because FAT has no journal and the page cache
   guarantees nothing. A synced file survived two power cuts
   intact. This is exactly why the download step uses the
   fsync-and-reverify design.
2. [x] Speaking EFI: init mounts efivarfs when the firmware is
   UEFI, then reads the boot variables. Each variable uses a
   small binary format called EFI_LOAD_OPTION: attributes, a
   UTF-16 name, a device path ending at a file on a partition,
   and free-form arguments that form a kernel command line. liken
   unit-tests the format against known-good bytes. One helper
   handles the immutable flag the kernel puts on every variable,
   and prints what it does.

   The lab gains OVMF: real UEFI firmware, split into read-only
   code shared by every guest, plus a per-node writable variable
   store. The store works like a motherboard's CMOS: boot entries
   live there and survive reboots. `make clean` removes the
   store, so a factory reset also clears firmware memory.

   Prove it: the console and Machine status report the firmware,
   BootCurrent, and a decoded BootOrder. This follows the console
   parity principle: what init prints must also reach Machine
   status.

   Proven on node-5 under OVMF: efivarfs mounted, mode reported as
   UEFI, an accurate "BootCurrent not set" for the direct-kernel
   boot, and OVMF's own default entries decoded by name into
   status.firmware. The codec also decoded every real entry on a
   physical laptop's firmware, including vendor-only ones.

   The EFI stub's initrd= argument is deprecated upstream, but
   liken still ships it. liken verifies the argument at the
   installer's first from-disk boot, the first moment a file-path
   boot exists to test it. No later step relies on the argument
   until that boot proves it works.
3. [x] Self-install, in the shape of a USB stick: `make install
   NODE=x` boots via -kernel one last time. QEMU serves as an
   installer stick or a PXE server. Init sees liken.install,
   claims the boot disk, verifies the release payload the
   installer carries, copies it into slot A, writes both boot
   entries and BootOrder, and powers off.

   install.cpio is liken.cpio with a second archive concatenated
   onto it, carrying vmlinuz, liken.cpio, and release.yaml. The
   kernel unpacks concatenated cpios, the same mechanism early
   microcode updates use. This means the installer's payload is
   byte-identical to what the digest chain describes.

   Each boot entry's baked command line carries the machine's
   name, its slot, and panic=10. The panic setting matters
   because a panicking trial kernel must reset into the
   firmware's fallback instead of hanging.

   `make run` becomes firmware-from-disk: no -kernel, no -append.
   (`run-once` keeps direct boot, because its oneshot knob cannot
   pass through a baked entry.)

   Prove it: a fresh node installs and boots to Ready from disk.
   Killing QEMU mid-install and re-running the install converges,
   because claiming skips already-claimed disks and copying
   re-verifies its work.

   Proven on node-5: the installer verified and copied both
   artifacts, wrote Boot0002 and Boot0003, and the firmware boot
   came up "booted via Boot0002 (liken slot A)" with the baked
   command line intact, rejoining the cluster Ready. This also
   resolved milestone risk 3: the EFI stub's initrd= argument
   works under OVMF. A second install run converged onto the same
   entry numbers, with everything re-verified in place. BOOT=disk
   stays a knob, not the default, until step 8 migrates the
   fleet.
4. [x] The releases domain and the API: `make release VERSION=x`
   rebuilds init, operator, and image with the overridden
   version stamp, into a separate build tree. The domain
   Makefiles learn overridable version and output knobs; the
   everyday dist/ trees are never touched. The build publishes
   releases/dist/<v>/, containing vmlinuz, liken.cpio,
   install.cpio, and release.yaml, which lists every artifact's
   sha256. The digest a catalog carries is the sha256 of that
   file's exact bytes.

   `make serve` is a small logged file server. Guests reach it at
   the host's NAT address. It serves as a release host on the
   internet.

   The Cluster gains spec.version and spec.releases (a source
   plus a catalog). CEL checks the target against catalog
   membership at admission. This check compares fields on the
   same object, so it can never wedge the way the storage rules
   once did. A machine with no slots reports NoSystemSlots
   instead of claiming it can comply.

   The fleet sweep computes status.releases.newest, using a
   hand-written semver comparison. The printer columns report
   the result plainly: the Cluster shows the target VERSION and
   the NEWEST version the catalog offers, and each Machine's
   LIKEN column shows the version it actually runs.

   Prove it: an edit whose target names no catalog entry is
   refused at admission, and `kubectl get clusters` shows target
   versus newest at a glance.

   Proven on the lab: liken published 0.1.0 and 0.2.0, with the
   stamp carried through the init binary, the operator image's
   name, and the DaemonSet's pin. The everyday dist/ trees stayed
   untouched. Setting spec.version with no catalog was refused at
   admission. Adding the catalog entry plus target in one edit
   made the VERSION column show 0.1.0. A bogus 9.9.9 target was
   refused even with a catalog present. `make corrupt` flipped
   one byte, and the published digest check failed exactly as
   designed. The publish step reuses the install payload's own
   release.yaml, so the stick's document and the server's
   document are byte-identical. NEWEST stays blank until a leader
   runs this build's operator, because the sweep is what writes
   it. The fleet migration in step 8 delivers that.
5. [x] The download: the operator fetches releases with an
   asynchronous fetcher. A blocking 116MB GET inside a reconcile
   pass would stop heartbeat updates during the download, and the
   machine would appear dead to the cluster. Milestone 10 taught
   this lesson; this design makes it structural.

   The fetcher streams each artifact through sha256 into the
   inactive slot. It writes each file as temp-and-rename, and
   resumes downloads through re-verification. FAT has no journal,
   so a torn download simply leaves files that fail their hash
   check; the fetcher fetches them again. Nothing is staged until
   every byte on the slot verifies against the catalog's chain.

   Downloading and DigestMismatch join the condition vocabulary.
   A down server is transient by definition, so the fetcher
   retries forever and records the reason in the condition
   message. A wrong digest sets the condition to Blocked until
   the catalog itself changes, and liken never stages that
   artifact.

   Prove it: the serve log shows the fetch. Killing the server
   mid-download and restarting it converges. A deliberately
   corrupted publish holds at DigestMismatch with nothing staged.

   Proven on node-5, which also learned to report which slot it
   booted from: liken.slot= now lands in status.boot.slot, and
   downloads target the other slot. The down-server drill
   surfaced a real bug: a failed fetch restarted on the next
   pass, so the Failed state lasted only between passes, and the
   condition always said "starting". The fix carries the previous
   failure's message into the retry; the drill then read
   "retrying after: connection refused". The corrupted 0.1.1
   fetched once in full, held at DigestMismatch/Blocked with the
   recovery spelled out (publish a corrected release under a new
   version), and never touched the network again. Retargeting
   clean 0.2.0 cleared the hold and converged as "1 artifacts
   fetched, the rest already verified in place". The two releases
   share a kernel, so this exercised resume-by-verification
   without a dedicated drill.

   The drills also taught two mixed-fleet lessons for step 8's
   migration. A leader's k3s restart re-applies the CRDs and
   DaemonSet baked into its own image. This pruned the new fields
   fleet-wide and left node-5's operator pod without its slots
   mount, until someone re-applied the new manifests by hand. The
   schema is part of the OS image, so a fleet upgrade is also a
   schema upgrade.
6. [x] The proving reboot: a verified download becomes a staged
   record in a third staging store, system/, alongside
   manifests/ and cluster/ on machineState. It uses the same
   four files and the same durable writes as those stores. Init's
   reboot path finds the staged record, writes the attempted
   marker and the firmware's BootNext (boot the other slot once),
   and reboots. The proving boot recognizes itself by
   liken.slot=.

   The operator's first reconcile on the new slot promotes the
   record, because an operator running as a pod proves that the
   new kernel, init, k3s, and the cluster join all work. Init
   flips BootOrder when promotion lands, and re-asserts BootOrder
   from the durable record on every boot after that. This means
   every power-cut gap in that timeline boots something already
   proven.

   Prove it: one Cluster edit upgrades one node, the LIKEN column
   flips, BootOrder prefers the new slot, and a plain reboot
   stays on the new version.

   Proven on node-5, where one catalog edit ran the whole chain
   unattended: the download, the staged record, the rollout
   granting its turn, the drain, "BootNext armed at Boot0003 ...
   once", the proving boot on slot B, promotion by the
   0.2.0-stamped operator's first pass, and the LIKEN column
   flipping to 0.2.0. A power cut landed by accident in exactly
   the gap this design accounts for, after promotion but before
   the BootOrder flip. The machine recovered on its own: it
   booted the old slot, re-staged, and re-proved. A deliberate
   power-cycle after that came up directly on slot B.

   One anomaly to watch in step 7's drills: the old-slot boot's
   BootOrder repair did not visibly fire. Its early returns
   printed nothing before; now they print their reasons, so a
   recurrence will explain itself.

   The catalog digest also proved worth including in the
   record's identity. Re-cutting 0.2.0 changed the digest, and
   the machine held at DigestMismatch until someone updated the
   catalog to match. The API, not the server, decides what runs.
7. [x] The fallbacks: init runs a proving-boot watchdog. It arms
   only when the running slot's record is still staged, and
   promotion disarms it. The watchdog uses a ten-minute timeout,
   the same RolloutStalled number the fleet already uses
   elsewhere. It reboots a machine that came up but never
   settled. The already-consumed BootNext sends that reboot back
   to the proven slot. There, the attempted marker records the
   failure as RejectedLastBoot. This does not cause a reboot
   loop, and the next version edit clears the rejection. A kernel
   that panics outright reaches the same outcome through
   panic=10, with no software involved at all.

   Prove it with two fault releases: one built to panic
   immediately (the firmware-fallback drill), and one built to
   wedge k3s (the watchdog drill). Both must end Ready on the old
   version, with the rejection visible in status.

   Proven on node-5, but only after the drills exposed a serious
   bug: the fallback the design depends on had never actually
   existed. BootOrder had never been rewritten after install,
   because promotion had never happened. The cluster's DaemonSet,
   applied by the old leaders, pinned the old operator image. So
   every proving boot ran the old operator, which correctly
   refused to promote a record that did not match its own version
   stamp. The convergence tidy-up judged the machine by init's
   version, read the machine as converged, and withdrew the
   trial's staging records. The proving watch also treated the
   staged file's absence as promotion. As a result, the first
   panic drill looped: 41 panics, with the fallback aimed at the
   panicking slot each time. This is exactly the loop this step
   must prevent.

   Three fixes addressed the root cause. Promotion now judges
   facts.version.liken, the version of the OS that actually
   booted, so the operator pod's own version no longer matters.
   The proving watch flips BootOrder only when the proven record
   matches its own trial. Withdrawal now clears the attempted
   marker. Arming is also hardened: fallbackInPlace re-asserts
   BootOrder and verifies it by reading it back, before any trial
   arms.

   The re-run went cleanly. Promotion printed its steps.
   "BootOrder now leads with Boot0003" was confirmed in the NVRAM
   file itself. A power-cycle booted slot B on the first try. The
   panic release then panicked exactly once and fell back. The
   wedge release sat unpromoted for its ten minutes, and then the
   watchdog rebooted it onto the proven slot. Both drills ended on
   the old version, with condition RejectedLastBoot and phase
   Blocked, serving the cluster the whole time. A retarget edit
   cleared each rejection. Machines report the standing rejection
   in status.boot.systemRejection.
8. [x] The fleet: migrate the five-node lab to disk boot, then
   run the full drill. One Cluster edit, a catalog append plus
   the target bump, rolls all five machines through milestone
   13's rollout. The cluster keeps serving throughout, and
   `kubectl get machines -o wide` shows the walk from
   AwaitingTurn to Ready on the new version.

   Migrating the pre-slot lab in place lost out to a rebuild. The
   old builds' operators could not even see the slot roles in
   their specs, and every old leader's k3s restart re-applied its
   baked CRDs, pruning the new schema fleet-wide. So the lab was
   factory-reset instead, and every node took the designed path:
   one `make install` and a firmware boot each. Five machines
   reached Ready in ninety seconds.

   The first full drill then found the milestone's last real bug.
   The operator's DaemonSet pinned a versioned image, so the
   first upgraded leader's manifests rolled a 0.2.1 pod onto a
   node still running 0.1.0. With imagePullPolicy: Never, that
   pod could not start. The rollout had just killed the one
   operator the machine needed to drive its own upgrade. The
   machine's own update mechanism had deadlocked it.

   The fix makes the operator pod genuinely part of the OS. Every
   release tags its build liken.sh/operator:installed, so one
   unchanging pod spec resolves, on each node, to that node's own
   baked image. The DaemonSet updates OnDelete, so applying
   manifests never kills a pod. The sweep leader's pod steward
   refreshes each machine's pod only after its upgrade lands.
   (operator/steward.go documents the design.)

   The proof was the 0.2.3 drill: one patch, zero manual actions.
   Five machines walked workers-first through the rollout in
   under four minutes, and every one flipped to its inactive
   slot. The steward refreshed all five operator pods behind
   them. A power cut afterward booted straight to the proven slot
   through its firmware entry, which verified the boot path
   itself, not just the outcome.
