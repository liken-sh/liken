# Declarative upgrades

Milestone 12. Completed. One field on the Cluster moves the whole
fleet to a new liken version.

Milestone 28 later changed two details in this document. A boot slot
now holds the generic OS and the deployment layer as separate files. A
boot entry now has two initrd= parameters. Machines now fetch
releases from liken's public channel, not from a per-deployment one.
The machinery this milestone built does not change: spec.version, the
catalog, the fetcher, the proving boot, and the BootOrder repair.
plans/completed/28-internet-updates.md has that design.

`spec.version` names the target version. It is a field on the Cluster,
not on the Machines, so an operator can retarget a fleet in one edit.
Each machine reports the version it runs in its status, where liken
records real state.

A machine that does not run the target version downloads the release.
It writes the release into the boot slot it is not running from. It
then reboots into that slot through milestone 13's rollout. The
rollout upgrades the workers first, then the leaders one at a time,
with no manual steps.

liken's OS is two files: vmlinuz and liken.cpio. An upgrade replaces
both files and reboots the machine. Two files make A/B slots and
roll-back-on-failed-boot simple.

liken has no bootloader. The kernel is an EFI executable, called the
stub, and the UEFI firmware's own boot menu selects the slot to boot.
The firmware's BootNext variable holds the "try the new slot once,
fall back if it fails" logic. The firmware does the fallback, and no
software is part of it.

Upgrades get their trust from explicit inputs, as the rest of liken
does. The Cluster holds a release source URL and a catalog. The
catalog maps each version to the digest of its release manifest. The
release manifest holds the sha256 of every artifact. The
verification chain runs from the API, to the catalog digest, to the
manifest, to the artifact bytes. liken adds no signatures until the
hardening tier.

liken reads the target and the catalog live. Neither one is
cluster-document drift. The sysctls set this precedent: an append to
the catalog must not cause a fleet-wide reboot.

Promotion uses the same pattern as the cluster document. Init boots
the staged slot tentatively, with an attempted marker and BootNext.
The operator's first reconcile on that slot is the proof that the boot
worked. After that, init re-asserts BootOrder from the durable record
on every boot. machineState is the authority for what boots, and the
firmware only caches that authority.

1. [x] The slot vocabulary and the FAT32 formatter: `systemA` and
   `systemB` join the storage roles at the head of the canonical
   order. The firmware is the first reader of any partition liken
   owns, so these roles come first. The GPT gives both roles the EFI
   System Partition type. They are the first roles whose partition
   type is not "Linux filesystem data", because the firmware finds a
   candidate slot by its type GUID.

   liken formats a slot with a hand-written FAT32 formatter, built in
   the same style as the GPT writer. It writes a boot sector that
   describes the geometry, two copies of the allocation table, and a
   root directory that is only cluster 2. FAT is the only filesystem
   that firmware is sure to read, so liken formats the slots as FAT32.
   The kernel's vfat module does the file I/O after that. Init now
   loads the modules before storage settles, because some roles depend
   on filesystem modules.

   Prove it: a node with a blank third disk claims both slots into
   status.storage, and a file written to a mounted slot is present
   after a reboot.

   Proven on node-5, with a live Machine edit that used the milestone
   13 rollout. The machine reported AwaitingTurn, received a reboot,
   printed the claim and both FAT32 formats to the console, and
   reported both slots as Partition-backed at 512Mi.

   The power-cut drill measured FAT's durability. An unsynced file
   written seconds before a power cut was gone after the cut, because
   FAT has no journal and the page cache gives no guarantee. A synced
   file was intact after two power cuts. The download step uses the
   fsync-and-reverify design for this reason.
2. [x] The EFI variables: init mounts efivarfs when the firmware is
   UEFI, then reads the boot variables. Each variable uses a small
   binary format, EFI_LOAD_OPTION: attributes, a UTF-16 name, a device
   path that ends at a file on a partition, and free-form arguments
   that make a kernel command line. liken unit-tests the format
   against known-good bytes. One helper handles the immutable flag
   that the kernel puts on every variable, and prints what it does.

   The lab uses OVMF, which is real UEFI firmware. OVMF is split into
   read-only code shared by every guest, plus a writable variable
   store for each node. The store is like a motherboard's CMOS: the
   boot entries are there and stay across reboots. `make clean`
   removes the store, so a factory reset also clears the firmware
   memory.

   Prove it: the console and the Machine status report the firmware,
   BootCurrent, and a decoded BootOrder. This follows the console
   parity principle: what init prints must also reach the Machine
   status.

   Proven on node-5 under OVMF: efivarfs mounted, the mode reported as
   UEFI, a correct "BootCurrent not set" for the direct-kernel boot,
   and OVMF's own default entries decoded by name into
   status.firmware. The codec also decoded every real entry in a
   physical laptop's firmware, including the vendor-only entries.

   The EFI stub's initrd= argument is deprecated upstream, but liken
   still uses it. liken verifies the argument at the installer's first
   from-disk boot, the first time a file-path boot is available to
   test it. No later step uses the argument before that boot proves
   it.
3. [x] Self-install, in the shape of a USB stick: `make install
   NODE=x` boots through -kernel one more time. QEMU is the installer
   stick or the PXE server. Init reads liken.install, claims the boot
   disk, verifies the release payload the installer carries, copies it
   into slot A, writes both boot entries and BootOrder, and powers
   off.

   install.cpio is liken.cpio with a second archive attached to the
   end, which holds vmlinuz, liken.cpio, and release.yaml. The
   kernel unpacks concatenated cpios, the same mechanism the early
   microcode updates use. The installer's payload is thus
   byte-identical to what the digest chain describes.

   Each boot entry's baked command line includes the machine's name,
   its slot, and panic=10. The panic setting is necessary because a
   trial kernel that panics must reset into the firmware's fallback
   and must not hang.

   `make run` now boots from disk through the firmware: no -kernel and
   no -append. `run-once` keeps the direct boot, because its oneshot
   knob cannot go through a baked entry.

   Prove it: a new node installs and boots to Ready from disk. If you
   kill QEMU during the install and run the install again, the machine
   converges, because the claim skips disks that are already claimed
   and the copy verifies its work again.

   Proven on node-5: the installer verified and copied both artifacts,
   wrote Boot0002 and Boot0003, and the firmware boot came up "booted
   via Boot0002 (liken slot A)" with the baked command line intact,
   and rejoined the cluster Ready. This also closed milestone risk 3:
   the EFI stub's initrd= argument works under OVMF. A second install
   run converged onto the same entry numbers, and verified everything
   again in place. BOOT=disk stays a knob and is not the default until
   step 8 migrates the fleet.
4. [x] The releases domain and the API: `make release VERSION=x`
   rebuilds init, the operator, and the image with the overridden
   version stamp, into a separate build tree. The domain Makefiles get
   overridable version and output knobs, and the everyday dist/ trees
   do not change. The build publishes releases/dist/<v>/, which
   contains vmlinuz, liken.cpio, install.cpio, and release.yaml.
   release.yaml lists the sha256 of every artifact. The digest in a
   catalog is the sha256 of that file's exact bytes.

   `make serve` is a small file server with a log. Guests reach it at
   the host's NAT address. It is the release host on the internet.

   The Cluster gets spec.version and spec.releases, which is a source
   plus a catalog. CEL checks the target against the catalog
   membership at admission. This check compares fields on the same
   object, so it cannot wedge the way the storage rules did. A machine
   with no slots reports NoSystemSlots and does not claim that it can
   comply.

   The fleet sweep computes status.releases.newest with a hand-written
   semver comparison. The printer columns report the result: the
   Cluster shows the target VERSION and the NEWEST version the catalog
   offers, and each Machine's LIKEN column shows the version it runs.

   Prove it: an edit whose target is not in the catalog is refused at
   admission, and `kubectl get clusters` shows the target and the
   newest version.

   Proven in the lab: liken published 0.1.0 and 0.2.0, and the stamp
   went through the init binary, the operator image's name, and the
   DaemonSet's pin. The everyday dist/ trees did not change. A
   spec.version with no catalog was refused at admission. One edit
   that added the catalog entry and the target made the VERSION column
   show 0.1.0. A 9.9.9 target was refused with the catalog present.
   `make corrupt` changed one byte, and the published digest check
   failed as designed. The publish step uses the install payload's own
   release.yaml, so the stick's document and the server's document are
   byte-identical. NEWEST is empty until a leader runs this build's
   operator, because the sweep writes it. Step 8's fleet migration
   does that.
5. [x] The download: the operator fetches releases with an
   asynchronous fetcher. A blocking 116MB GET inside a reconcile pass
   stops the heartbeat updates during the download, and the cluster
   reads the machine as dead. Milestone 10 showed this problem, and
   this design prevents it.

   The fetcher streams each artifact through sha256 into the inactive
   slot. It writes each file as temp-and-rename, and resumes a
   download through verification. FAT has no journal, so a torn
   download leaves files that fail their hash check, and the fetcher
   gets them again. liken stages nothing until every byte on the slot
   verifies against the catalog's chain.

   Downloading and DigestMismatch join the condition vocabulary. A
   server that is down is transient, so the fetcher retries forever
   and records the reason in the condition message. A wrong digest
   sets the condition to Blocked until the catalog itself changes, and
   liken never stages that artifact.

   Prove it: the serve log shows the fetch. If you kill the server
   during a download and start it again, the fetch converges. A
   corrupted publish holds at DigestMismatch with nothing staged.

   Proven on node-5, which also reports the slot it booted from:
   liken.slot= lands in status.boot.slot, and the downloads go to the
   other slot. The down-server drill found a bug: a failed fetch
   restarted on the next pass, so the Failed state lasted only between
   passes and the condition always said "starting". The fix keeps
   the previous failure's message in the retry, and the drill then
   read "retrying after: connection refused". The corrupted 0.1.1
   fetched once in full, held at DigestMismatch/Blocked with the
   recovery in the message (publish a corrected release under a new
   version), and did not use the network again. A retarget to the
   clean 0.2.0 cleared the hold and converged as "1 artifacts fetched,
   the rest already verified in place". The two releases share a
   kernel, so this exercised resume-by-verification without a separate
   drill.

   The drills also showed how a mixed fleet behaves, which step 8's
   migration must handle. A leader's k3s restart re-applies the CRDs
   and the DaemonSet baked into its own image. This pruned the new
   fields across the fleet and left node-5's operator pod without its
   slots mount, until someone applied the new manifests again by hand.
   The schema is part of the OS image, so a fleet upgrade is also a
   schema upgrade.
6. [x] The proving reboot: a verified download becomes a staged record
   in a third staging store, system/, next to manifests/ and cluster/
   on machineState. It uses the same four files and the same durable
   writes as those stores. Init's reboot path finds the staged record,
   writes the attempted marker and the firmware's BootNext (boot the
   other slot one time), and reboots. The proving boot identifies
   itself by liken.slot=.

   The operator's first reconcile on the new slot promotes the record.
   An operator that runs as a pod is proof that the new kernel, init,
   k3s, and the cluster join all work. Init flips BootOrder when the
   promotion lands, and re-asserts BootOrder from the durable record
   on every boot after that. A power cut at any point in that timeline
   boots something that is already proven.

   Prove it: one Cluster edit upgrades one node, the LIKEN column
   changes, BootOrder prefers the new slot, and a plain reboot stays
   on the new version.

   Proven on node-5, where one catalog edit ran the full chain with no
   manual steps: the download, the staged record, the rollout that
   granted its turn, the drain, "BootNext armed at Boot0003 ... once",
   the proving boot on slot B, the promotion by the 0.2.0-stamped
   operator's first pass, and the LIKEN column changing to 0.2.0. A
   power cut occurred by accident in the gap this design accounts for,
   after the promotion but before the BootOrder flip. The machine
   recovered without help: it booted the old slot, staged again, and
   proved again. A deliberate power cycle after that came up directly
   on slot B.

   One anomaly for step 7's drills: the old-slot boot's BootOrder
   repair did not visibly run. Its early returns printed nothing
   before; they now print their reasons, so a recurrence will show
   why.

   The catalog digest is also part of the record's identity. A re-cut
   of 0.2.0 changed the digest, and the machine held at DigestMismatch
   until someone updated the catalog to match. The catalog in the API,
   not the release server, controls what runs.
7. [x] The fallbacks: init runs a proving-boot watchdog. It arms only
   when the running slot's record is still staged, and the promotion
   disarms it. The watchdog uses a ten-minute timeout, the same
   RolloutStalled number the fleet uses elsewhere. It reboots a
   machine that came up but did not settle. The BootNext is already
   consumed, so that reboot goes back to the proven slot. There, the
   attempted marker records the failure as RejectedLastBoot. This does
   not make a reboot loop, and the next version edit clears the
   rejection. A kernel that panics gets the same result through
   panic=10, with no software involved.

   Prove it with two fault releases: one built to panic immediately
   (the firmware-fallback drill), and one built to wedge k3s (the
   watchdog drill). Both must end Ready on the old version, with the
   rejection visible in the status.

   Proven on node-5, but only after the drills found a bug: the
   fallback the design depends on had never worked. BootOrder was
   never rewritten after the install, because the promotion never
   happened. The cluster's DaemonSet, applied by the old leaders,
   pinned the old operator image. Every proving boot therefore ran the
   old operator, which correctly refused to promote a record that did
   not match its own version stamp. The convergence tidy-up judged the
   machine by init's version, read the machine as converged, and
   withdrew the trial's staging records. The proving watch also read
   the staged file's absence as a promotion. The first panic drill
   looped: 41 panics, with the fallback aimed at the panicking slot
   each time. This is the loop this step must prevent.

   Three fixes correct this. Promotion now uses facts.version.liken,
   the version of the OS that booted, so the operator pod's own
   version no longer matters. The proving watch flips BootOrder only
   when the proven record matches its own trial. Withdrawal now clears
   the attempted marker. The arming is also stronger: fallbackInPlace
   re-asserts BootOrder and verifies it with a read-back, before any
   trial arms.

   The re-run was clean. Promotion printed its steps. "BootOrder now
   leads with Boot0003" was confirmed in the NVRAM file itself. A
   power cycle booted slot B on the first try. The panic release then
   panicked one time and fell back. The wedge release stayed
   unpromoted for its ten minutes, and the watchdog then rebooted it
   onto the proven slot. Both drills ended on the old version, with
   the condition RejectedLastBoot and the phase Blocked, and served
   the cluster the whole time. A retarget edit cleared each rejection.
   Machines report the standing rejection in
   status.boot.systemRejection.
8. [x] The fleet: migrate the five-node lab to disk boot, then run the
   full drill. One Cluster edit, a catalog append plus the target
   change, rolls all five machines through milestone 13's rollout. The
   cluster continues to serve, and `kubectl get machines -o wide`
   shows the change from AwaitingTurn to Ready on the new version.

   A rebuild replaced an in-place migration of the pre-slot lab. The
   old builds' operators could not read the slot roles in their specs,
   and every old leader's k3s restart re-applied its baked CRDs, which
   pruned the new schema across the fleet. The lab was factory-reset
   instead, and every node took the designed path: one `make install`
   and one firmware boot each. Five machines reached Ready in ninety
   seconds.

   The first full drill then found the last bug in the milestone. The
   operator's DaemonSet pinned a versioned image, so the first
   upgraded leader's manifests rolled a 0.2.1 pod onto a node that
   still ran 0.1.0. With imagePullPolicy: Never, that pod could not
   start. The rollout had killed the one operator the machine needed
   to drive its own upgrade, and the upgrade mechanism thus blocked
   itself.

   The fix makes the operator pod part of the OS. Every release tags
   its build liken.sh/operator:installed, so one unchanging pod spec
   resolves, on each node, to that node's own baked image. The
   DaemonSet updates OnDelete, so an apply of the manifests never
   kills a pod. The sweep leader's pod steward refreshes each
   machine's pod only after its upgrade lands. operator/steward.go
   documents the design.

   The 0.2.3 drill is the proof: one patch and no manual actions. Five
   machines walked workers-first through the rollout in less than four
   minutes, and each one changed to its inactive slot. The steward
   refreshed all five operator pods behind them. A power cut after
   that booted directly to the proven slot through its firmware entry,
   which verified the boot path and not only the outcome.
