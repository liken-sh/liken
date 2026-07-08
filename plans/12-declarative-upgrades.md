# Declarative upgrades

Milestone 12 — Done

Declarative upgrades: one field on the Cluster moves the whole
fleet to a new liken version. `spec.version` names the target.
It lives on the Cluster, not the Machines, because a fleet
should be retargeted in one edit; a machine's version belongs
in its status, where reality is reported, not in its spec.
Machines that aren't running the target download the release,
write it into the boot slot they are *not* running from, and
reboot into it through milestone 13's rollout: workers first,
leaders one at a time, with no human supervising. For a
two-file OS an upgrade really is "replace vmlinuz and
liken.cpio and reboot", which makes A/B slots and
roll-back-on-failed-boot the natural shape. It also finally
answers the bootloader question QEMU's `-kernel` flag has been
deferring, and the answer is that there is no bootloader: the
kernel is already an EFI executable (the stub), UEFI
firmware's own boot menu picks a slot, and "try the new one
once, fall back if it fails" is the firmware's BootNext, so
fallback is handled by the firmware itself rather than by
software. Trust stays where liken's trust already lives, in
explicit inputs: the Cluster carries a release source URL and
a catalog mapping each version to the digest of its release
manifest, which in turn carries every artifact's sha256. The
verification chain runs from the API to the catalog digest to
the manifest to the bytes, with no signatures until the
mastery tier. The target and catalog are read live and never
count as cluster-document drift (the sysctls precedent: a
catalog append must not stage a fleet-wide reboot). Promotion
mirrors the cluster document exactly: boot the staged slot
tentatively (attempted marker + BootNext), let the operator's
first reconcile be the proof, and let init re-assert BootOrder
from the durable record every boot. machineState is the
authority; the firmware is a cache of it.
1. [x] The slot vocabulary and the FAT32 formatter: `systemA` and
   `systemB` join the storage roles at the head of the
   canonical order (the firmware is the earliest reader of any
   partition liken owns), typed in the GPT as EFI System
   Partitions. They are the first roles whose partition type
   isn't "Linux filesystem data", because the type GUID is
   precisely how firmware recognizes a candidate. Formatting
   is a hand-written FAT32 formatter in the same style as the
   GPT writer (a boot sector describing the geometry, two
   copies of the allocation table, a root directory that is
   just cluster 2), because FAT is the only filesystem
   firmware promises to read. The kernel's vfat module handles
   file I/O afterward, which moves module loading ahead of
   storage settling in init, since some roles' filesystems are
   now modules. Prove it: a node with a blank third disk
   claims both slots into status.storage, and a file written
   to a mounted slot survives a reboot. Proven on node-5 via a
   live Machine edit that rode the milestone-13 rollout: the
   machine reported AwaitingTurn, was granted a reboot,
   printed the claim and both FAT32 formats to the console,
   and reported both slots Partition-backed at 512Mi. The
   power-cut drill taught the milestone's next lesson early:
   an *unsynced* file written seconds before a power cut was
   gone afterward, because FAT has no journal and the page
   cache guarantees nothing, while a synced file survived two
   power cuts intact. The download step's fsync-and-reverify
   design exists for exactly this reason.
2. [x] Speaking EFI: init mounts efivarfs when the firmware is
   UEFI and reads the boot variables. Each is a small binary
   format (EFI_LOAD_OPTION: attributes, a UTF-16 name, a
   device path ending at a file on a partition, and free-form
   arguments that are exactly a kernel command line),
   unit-tested against known-good bytes, with the immutable
   flag the kernel puts on every variable handled in one
   helper that prints what it does. The lab gains OVMF: real
   UEFI firmware, split as read-only code shared by every
   guest plus a per-node writable variable store. The store is
   the equivalent of a motherboard's CMOS, where boot entries
   live and survive reboots; `make clean` removes it, so a
   factory reset clears firmware memory too. Prove it: the
   console and Machine status report the firmware,
   BootCurrent, and a decoded BootOrder (console parity as
   always). Proven on node-5 under OVMF: efivarfs mounted,
   mode UEFI, an accurate "BootCurrent not set" for the
   direct-kernel boot, and OVMF's own default entries decoded
   by name into status.firmware. The codec also decoded every
   real entry on a physical laptop's firmware, including
   vendor-only ones. The EFI stub's initrd= argument is
   deprecated upstream but still shipped; it gets verified at
   the installer's first from-disk boot, the first moment a
   file-path boot exists to test, and nothing beyond that
   boot builds on it untested.
3. [x] Self-install, in the shape of a USB stick: `make install
   NODE=x` boots via -kernel one last time, with QEMU standing
   in for an installer stick or a PXE server. Init, seeing
   liken.install, claims the boot disk, verifies the release
   payload the installer carries, copies it into slot A,
   writes both boot entries and BootOrder, and powers off.
   install.cpio is liken.cpio with a second archive
   concatenated on, carrying vmlinuz, liken.cpio, and
   release.yaml. The kernel unpacks concatenated cpios (the
   same mechanism early microcode updates use), so the
   installer's payload is byte-identical to what the digest
   chain describes. Each entry's baked command line carries
   the machine's name, its slot, and panic=10. The panic
   setting matters because a panicking trial kernel must
   reset into the firmware's fallback rather than hang. `make
   run` becomes firmware-from-disk: no -kernel, no -append.
   (`run-once` keeps direct boot, because its oneshot knob
   can't be passed through a baked entry.) Prove it: a fresh
   node installs and boots to Ready from disk; killing QEMU
   mid-install and re-running converges, since claiming skips
   claimed disks and copying re-verifies. Proven on node-5:
   the installer verified and copied both artifacts, wrote
   Boot0002/Boot0003, and the firmware boot came up "booted
   via Boot0002 (liken slot A)" with the baked command line
   intact, rejoining the cluster Ready. That also answered
   milestone risk 3: the EFI stub's initrd= argument works
   under OVMF. A second install run converged onto the same
   entry numbers with everything re-verified in place.
   BOOT=disk is a knob rather than the default until step 8
   migrates the fleet.
4. [x] The releases domain and the API: `make release VERSION=x`
   rebuilds init, operator, and image with the overridden
   version stamp into a separate build tree (the domain
   Makefiles learn overridable version and output knobs; the
   everyday dist/ trees are never touched) and publishes
   releases/dist/<v>/: vmlinuz, liken.cpio, install.cpio, and
   release.yaml listing every artifact's sha256. The digest a
   catalog carries is the sha256 of that file's exact bytes.
   `make serve` is a small logged file server the guests reach
   at the host's NAT address, the lab's stand-in for a release
   host on the internet. The Cluster grows spec.version and
   spec.releases (source plus catalog). CEL holds the target
   to catalog membership at admission; this is a same-object
   check, so it can never wedge the way the storage rules once
   did. A machine with no slots reports NoSystemSlots rather
   than claiming it can comply. The fleet sweep computes
   status.releases.newest (a hand-written semver comparison),
   and the printer columns report it plainly: the Cluster
   shows the target VERSION and the NEWEST the catalog offers,
   while each Machine's LIKEN column shows the version it is
   actually running. Prove it: an edit whose target names no
   catalog entry is refused at admission, and `kubectl get
   clusters` shows target versus newest at a glance. Proven on
   the lab: 0.1.0 and 0.2.0 published with the stamp carried
   through the init binary, the operator image's name, and the
   DaemonSet's pin, with the everyday dist/ trees untouched.
   Setting
   spec.version with no catalog was refused at admission, the
   catalog entry plus target landed in one edit and the
   VERSION column showed 0.1.0, and a bogus 9.9.9 target was
   refused even with a catalog present. `make corrupt` flipped
   one byte and the published digest check failed exactly as
   designed. The publish reuses the install payload's own
   release.yaml, so the stick's document and the server's are
   byte-identical. NEWEST stays blank until a leader runs this
   build's operator, since the sweep is what writes it; the
   fleet migration in step 8 delivers that.
5. [x] The download: an asynchronous fetcher in the operator,
   because a blocking 116MB GET inside a reconcile pass would
   starve the heartbeat and read as a death (milestone 10's
   lesson, made structural). It streams each artifact through
   sha256 into the inactive slot, writes temp-and-rename, and
   resumes by re-verification: FAT has no journal, so a torn
   download is just files that fail their hashes and are
   fetched again. Nothing is ever staged until every byte on
   the slot verifies against the catalog's chain. Downloading
   and DigestMismatch join the condition vocabulary. A down
   server is transient by definition, so the fetcher retries
   forever with the reason in the condition message; a wrong
   digest is Blocked until the catalog itself changes, and is
   never staged. Prove it: the serve log shows the fetch;
   killing the server mid-download and restarting it
   converges; a deliberately corrupted publish holds at
   DigestMismatch with nothing staged. Proven on node-5,
   which also learned to report which slot it booted from:
   liken.slot= now lands in status.boot.slot, and downloads
   aim at the other one. The down-server drill surfaced a
   real bug: a failed fetch restarts on the next pass, so the
   Failed state lived only between passes and the condition
   forever said "starting". Now the retry carries the
   previous failure's message, and the drill read "retrying
   after: connection refused". The corrupted 0.1.1 fetched
   once in full, held at DigestMismatch/Blocked with the
   recovery spelled out (publish a corrected release under a
   new version), and never touched the network again.
   Retargeting clean 0.2.0 cleared the hold and converged as
   "1 artifacts fetched, the rest already verified in place":
   the two releases share a kernel, so resume-by-verification
   was exercised without a dedicated drill. The drills also
   taught two mixed-fleet lessons for step 8's migration: a
   leader's k3s restart re-applies the CRDs and DaemonSet
   baked into *its* image, which pruned the new fields
   fleet-wide and left node-5's operator pod without its
   slots mount until the new manifests were re-applied by
   hand. The schema is part of the OS image, so a fleet
   upgrade is also a schema upgrade.
6. [x] The proving reboot: a verified download becomes a staged
   record in a third staging store, system/ beside manifests/
   and cluster/ on machineState, with the same four files and
   the same durable writes. Init's reboot path finds it, writes
   the attempted marker and the firmware's BootNext (boot the
   other slot, once), and reboots. The proving boot recognizes
   itself by liken.slot=. The operator's first reconcile
   promotes the record, since an operator running as a pod
   proves that the new kernel, init, k3s, and the join all
   work. Init flips BootOrder when promotion lands, and
   re-asserts it from the durable record on every boot
   thereafter. Every power-cut gap in that timeline boots
   something proven. Prove it: one Cluster edit upgrades one
   node, the LIKEN column flips, BootOrder prefers the new
   slot, and a plain reboot stays on the new version. Proven
   on node-5, where one catalog edit ran the whole chain
   unattended: the download, the staged record, the rollout
   granting its turn, the drain, "BootNext armed at Boot0003
   ... once", the proving boot on slot B, promotion by the
   0.2.0-stamped operator's first pass, and the LIKEN column
   flipping to 0.2.0. A power cut landed by accident in
   exactly the gap the design worries about, after promotion
   but before the BootOrder flip, and the machine recovered on
   its own: it booted the old slot, re-staged, re-proved, and
   a deliberate power-cycle after that came up directly on
   slot B. One anomaly to watch in step 7's drills: the
   old-slot boot's BootOrder repair didn't visibly fire. Its
   early returns printed nothing; they now print their
   reasons, so a recurrence will explain itself. The catalog
   digest also proved worth including in the record's
   identity: re-cutting 0.2.0 changed the digest, and the
   machine held at DigestMismatch until the catalog was
   updated to match. The API, not the server, decides what
   runs.
7. [x] The fallbacks: a proving-boot watchdog in init, armed only
   when the running slot's record is still staged and disarmed
   by promotion, with a ten-minute timeout (the fleet's
   established RolloutStalled number). It reboots a machine
   that came up but never settled, and the already-consumed
   BootNext lands that reboot back on the proven slot, where
   the attempted marker records the failure: RejectedLastBoot,
   no reboot loop, cleared by the next version edit. A kernel
   that panics outright reaches the same outcome through
   panic=10, with no software involved at all. Prove it with
   two fault releases: one built to panic immediately (the
   firmware-fallback drill) and one built to wedge k3s (the
   watchdog drill), both ending Ready on the old version with
   the rejection visible in status. Proven on node-5, but only
   after the drills exposed the milestone's deepest bug: the
   fallback they depend on had never actually existed.
   BootOrder had never once been rewritten after install,
   because promotion had never once happened. The cluster's
   DaemonSet, applied by the old leaders, pins the old
   operator image, so every proving boot ran the *old*
   operator, which rightly refused to promote a record that
   didn't match its own version stamp. The convergence
   tidy-up, which judged by init's version, then read the
   machine as converged and withdrew the trial's staging
   records. And the proving watch treated the staged file's
   absence as promotion. The first panic drill therefore
   looped: 41 panics, with the fallback aimed at the panicking
   slot, exactly the loop this step exists to forbid. Three
   fixes at the root: promotion now judges
   facts.version.liken, the version of the OS that actually
   booted, so the operator pod's own version is irrelevant;
   the proving watch flips BootOrder only when the proven
   record matches its own trial; and withdrawal clears the
   attempted marker. Arming is also hardened: fallbackInPlace
   re-asserts BootOrder and verifies it by readback before any
   trial arms. The re-run went cleanly: promotion printed its
   steps, "BootOrder now leads with Boot0003" was confirmed in
   the NVRAM file itself, and a power-cycle booted slot B on
   the first try. Then the panic release panicked exactly once
   and fell back, and the wedge release sat unpromoted for its
   ten minutes before the watchdog rebooted it onto the proven
   slot. Both drills ended on the old version,
   RejectedLastBoot, phase Blocked, serving the cluster the
   whole time, and a retarget edit cleared each rejection.
   Machines report the standing rejection in
   status.boot.systemRejection.
8. [x] The fleet: migrate the five-node lab to disk boot, then
   run the full drill: one Cluster edit (a catalog append and
   the target bump) rolls all five machines through milestone
   13's rollout, with the cluster serving throughout and
   `kubectl get machines -o wide` showing the walk from
   AwaitingTurn to Ready on the new version. Migrating the
   pre-slot lab in place lost out to a rebuild: the old
   builds' operators couldn't even see the slot roles in
   their specs, and every old leader's k3s restart re-applied
   its baked CRDs, pruning the new schema fleet-wide. So the
   lab was factory-reset and every node took the designed
   path instead, one `make install` and a firmware boot each,
   and five machines were Ready in ninety seconds. The first
   full drill then found the milestone's last real bug: the
   operator's DaemonSet pinned a versioned image, so the
   first upgraded leader's manifests rolled a 0.2.1 pod onto
   a node still running 0.1.0. With imagePullPolicy: Never,
   that pod could not start, and the rollout had just killed
   the one operator the machine needed to drive its own
   upgrade. The machine was deadlocked by its own update
   mechanism. The fix makes the operator pod genuinely part
   of the OS: every release tags its build
   liken.sh/operator:installed, so one unchanging pod spec
   resolves per-node to that node's own baked image; the
   DaemonSet updates OnDelete, so applying manifests never
   kills a pod; and the sweep leader's pod steward refreshes
   each machine's pod only after its upgrade lands
   (operator/steward.go documents the design). The proof was
   the 0.2.3 drill: one patch, zero manual actions. Five
   machines walked workers-first through the rollout in under
   four minutes, every one flipping to its inactive slot, and
   the steward refreshed all five operator pods behind them.
   A power cut afterward booted straight to the proven slot
   via its firmware entry, which verified the boot path
   itself and not just the outcome.
