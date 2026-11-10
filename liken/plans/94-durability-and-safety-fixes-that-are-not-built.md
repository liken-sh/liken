# 94. Durability and safety fixes that are not built

Milestone 94. Proposed 2026-11-10. Nothing is built. It holds every
fix of
[milestone 66](completed/66-durability-and-safety.md) that was not
built when that milestone closed. A survey of the tree on 2026-09-11
found a set of places where `liken`'s own durability and safety rules
do not hold under a power cut, a failed read, or a build that stopped
early. Milestone 66 holds that survey, the dated corrections of
2026-10-10, and the evidence of each fix that it built.

## What is built

Milestone 66 built the orphan reaper's bound, a deadline on every
request of the operators' API client, the release downloads that
finish or stop (in
[milestone 73](completed/73-a-staged-slot-stays-the-release-it-names.md)),
the refusal to offer disks when a machine's facts do not read, the full
`status.boot.network` schema, the pipe leak, the crash batch, the one
writer of the k3s log, the two smaller cases of part three, the
condition roll-up, the commit pin of every GitHub Action, and `mke2fs`
in the image's list of programs. Its status paragraph of 2026-11-10
names the commits.

## The rule this milestone enforces

Every fix below follows one rule. When the code cannot confirm a
state, it must refuse or report, and never act. So a trial whose arm
write may not have reached the disk does not count as armed, a failed
read does not count as an empty result, and a condition that this
pass did not check is not stamped as current again.

## The boot chain's writes reach the disk

**The BIOS arm has no device flush.** `armTrial` and `assertProven` in
`init/grubactuator.go` write `try_slot` and `default_slot` through
`updateGRUBEnv` (`init/grubenv.go:111`) and `writeFileDurably`
(`init/durable.go`), which fsyncs the temp file and renames it. Nothing
flushes the block device after the rename. On FAT, the rename's
directory entry is in buffers attached to the block device, and an
fsync of the directory does not reach them. `init/slotloader.go`
explains this at length, and the lab found it on the first promotion
drill of the built part of plan 33. The GRUB arm takes the same path
with no flush. A power cut after the arm can lose the arm write while
the attempted marker remains, and the next boot reads that as a
release that ran and fell back. The `grub.cfg` heal in the same file
(`healBootChain`, which writes `grub.cfg` through `writeFileDurably` at
`init/grubactuator.go:178`) has the same gap, and a boot home with a
`.partial` file and no `grub.cfg` drops GRUB to a rescue prompt.

The reboot path does flush. `rebootMachine` (`init/reboot.go`) arms
the trial, shuts the plane down, remounts each disk read-only, and
calls `unix.Sync()` (`init/reboot.go:218`) before the reboot syscall.
The gaps are the promotion's write of `default_slot`, which stays
unflushed until the kernel writes it back, and the `grub.cfg` heal.

The fix is `unix.Sync()` after each of those writes, which is what the
installer, the loader writer, and the report already do
(`init/install.go:182`, `init/slotloader.go:123`, `init/report.go:483`).

**The readback after that write reads the page cache.** `assertProven`
re-reads what it just wrote, and its comment says this check is as
reliable as the UEFI dialect's readback of `BootOrder`. The UEFI
readback goes through efivarfs to the firmware. This one comes back
from the page cache. The readback is useful only after the flush
above.

**Two comments state opposite rules about FAT.** The comment on
`syncDirectory` in `init/durable.go` (lines 76 to 81) says a FAT
directory fsync flushes the whole block device, so one call covers
every rename. `init/slotloader.go` says the opposite, and the lab
showed that the second one is correct. `installGRUB` in
`init/install.go` follows the first one at its `grub.cfg` write and
calls only `syncDirectory` (`init/install.go:255`). The wrong comment
says one directory fsync is enough, so code that follows it keeps
adding new call sites without a flush. This milestone deletes it and
makes every FAT writer call `unix.Sync()`.

The install sequence calls `unix.Sync()` after `installGRUB` returns
and before it reports success (`init/install.go:182`), so the installed
`grub.cfg` reaches the disk. Only the local comment and call are wrong.

**The primary and backup GPT have no barrier between them.**
`WriteTableInPlace` (`disks/gpt.go:463`) writes all five chunks of both
tables and calls `Sync` once at the end. A power cut or a drive that
reorders writes can leave the primary entries half-written while the
backup is already overwritten, and then `ReadGPT` reports neither copy
readable. The first fix proposed was one `Sync` after the primary
table and one after the backup, so that at every moment one complete
table exists.

That order does not protect a relocation. When the disk grew, the new
backup goes to the new end of the disk, where `ReadGPT` looks for it,
so a torn primary write after it leaves no readable copy. The safe
order is the backup first, a `Sync`, then the primary and a `Sync`. The
single `Sync` is in `writeTableBytes` (`disks/gpt.go:509`), so
`WriteTable` has the same gap.

**`LastUsableLBA` is off by one.** The comment on the constants in
`disks/gpt.go` (lines 71 to 75) says the last usable sector is 34
sectors from the end, and `LastUsableLBA` (`disks/gpt.go:94`) returns
the one 35 from the end. The test (`TestGPTLastUsableLBA`,
`disks/gpt_test.go:21`) pins the wrong value. This is conservative and
loses one sector, and it makes `liken`'s tables disagree with every
other partitioner. Fix the function and the test together.

The fix alone is not safe. Every existing remainder partition ends 35
sectors from the end, so after the fix `planGrowth` (`init/grow.go:54`)
plans a one-sector growth on every machine. On the system disk the
kernel refuses to read the new table while the boot slot is mounted,
and storage fails. The fix needs a tolerance of one sector in
`planGrowth`, and a test that a remainder at the old end stays
untouched.

## init supervises processes safely

**The wpa_supplicant control channel can panic on close.**
`wpaControl.close` (`init/wpactrl.go:355`) closes the event channel
under the lock, and the read goroutine (`read`, `init/wpactrl.go:328`)
sends on that channel without the lock. Closing the socket first does
not stop a goroutine that is already past `Read`. A send on a closed
channel panics, also inside a `select` with a `default`. The fix is a
done channel the reader checks, or a close that waits for the reader.

`close` has one caller, for a radio that settles after the shutdown
began, so this can happen at most once, during a shutdown.

**Two waits have no bound after SIGKILL.** `stopK3s`
(`init/supervisor.go:466`) receives on the death channel with no
timeout after the kill (`init/supervisor.go:475`). `stopSupplicant`
(`init/wireless.go:537`) has the same receive (`init/wireless.go:545`).
Both receives get a deadline, and a miss is printed and reported.

`stopK3s` runs before `rebootMachine`, while the reaper still runs.
The hang it can meet is a k3s in uninterruptible sleep, such as on hung
storage, that SIGKILL cannot end, and then a requested reboot never
reaches the reboot syscall. `stopSupplicants` already bounds each
supplicant's stop, so only `endSupplicant` (`init/wireless.go:320`) at
bring-up waits with no bound. A missed wait on the restart path must
not start a second k3s beside the first, so the deadline needs a
decision on what a miss does there. "Open questions" below holds it.

**The proving watchdog blocks its own shutdown.** `provingWatch`
(`init/proving.go`) calls `rebootMachine` from inside a machine-plane
component (`init/proving.go:292`). `rebootMachine` shuts the plane down
and waits on the plane's wait group, which counts the goroutine that
called it. Every watchdog reboot waits the full ten-second shutdown
timeout and then prints that the proving watch did not stop. The watch
should signal the reboot and return, so the plane can stop it like any
other component. `provingWatch` has a test of its promotion path
(`init/provingwatch_test.go`) and none of its watchdog reboot, which
gets one here.

This path also skips `stopK3s`, so `superviseK3s` starts k3s again
during the ten-second shutdown, and the new k3s keeps `clusterState`
open. A watch that sends the reboot through `rebootRequests` would also
stop the `wedge-k3s` fault drill, whose main goroutine reads no
requests. And it would pass through `stopK3s`, whose wait after SIGKILL
has no bound, on the boot that a stuck k3s most likely caused. The
bound above comes first.

**The node password is minted after a step that can fail.**
`persistNodePassword` (`init/k3s.go:603`) returns early when the
symlink write fails (`init/k3s.go:616`), and `mintNodePassword` never
runs. That mint exists to stop k3s from writing its own password with
a torn-write window that locks a machine out of its cluster.

Minting first does not help. When the symlink fails, k3s reads its
password on the RAM root, not the minted file, and on a first join the
next boot would present a password that the leader never recorded. The
symlink fails only when its path already exists, so the fix accepts a
symlink that already names the same directory, and treats any other
failure as a boot fault.

## The operators refuse instead of guessing

**An empty device walk deletes the ResourceSlice.**
`SliceWriter.Write` (`kubernetes/resourceslices.go:245`), called from
`machine-operator/dra.go:112`, deletes the slice when the device list
is empty (`kubernetes/resourceslices.go:270`). `usbDevices` and
`boardDevices` (`hardware/inventory.go:52` and `:121`) turn an
unreadable directory into an empty list, and `DiscoverDevices`
(`hardware/sysfs.go:95`) does the same for a bus directory that does
not read. A machine with no devices and a machine whose sysfs could not
be read are the same input. The fix is a type change: the walk returns
an error, and the slice writer refuses to delete on an error. Whether
a failed walk keeps the last offer or withdraws it needs a decision:
this milestone's rule withholds an offer it cannot show is safe.
"Open questions" below holds it.

**The drain is skipped on any failed Node read.** This is a bug of
medium priority. `disruptions.gate` (`machine-operator/disruptions.go`)
calls `gateThroughDrain` only when a reboot is requested, the conductor
granted a turn, and the Node read succeeded
(`machine-operator/disruptions.go:69`). On a timeout, a server error,
or any other read failure, it leaves `requestReboot` set. The comment
gives demotion as the reason, when there is no Node to cordon. The
condition does not distinguish that lifecycle state from an API error
on a node that still runs workloads.

A 500 or a timeout on one pass has the same effect: an approved reboot
skips eviction. The machine then follows its shutdown sequence without
the Eviction API, so no `PodDisruptionBudget` protects its workloads.
The convergence still reports the normal reboot-requested result, and
it reports no drain-read failure.
`TestAGrantedRebootWithNoNodeSkipsTheDrain`
(`machine-operator/disruptions_test.go:16`) asserts the skip on
purpose, with a server error as its input.

A temporary fixture called the gate with a read error, a granted turn,
and a convergence that requested a reboot. The result still requested
the reboot, and the gate made no call to the drain client. This
verifies only the gate's behavior. No live eviction or reboot drill
ran. The defect is the decision to skip the drain, not the eviction
step in `machine-operator/drain.go`.

The fix holds the reboot when the Node read fails, unless an explicit,
expected lifecycle state allows the bypass. A 404 during a demotion
skips the drain. A bare 404 does not show that demotion caused the
absence. Any other error stops the reboot, retries the read, and
reports why through the convergence condition and `NodeObserved`
below.

The demotion path keeps its order. `carryOutDemotion`
(`machine-operator/demotion.go:105`) writes its reboot intent before it
deletes the Node, because the deletion can terminate its own pod. That
intent uses the runtime channel on tmpfs, so it does not survive a
power loss. The fix must not strand a machine whose Node was removed
on purpose.

The fix needs no new user-facing disruption policy. The five-minute
`drainDeadline` (`machine-operator/drain.go:65`) allows a reboot on
purpose when workloads do not leave in time. The fix does not change
that limit, `rebootPolicy`, or the conductor's grant rules. Whether a
`PodDisruptionBudget` should block a reboot with no limit is a separate
policy question.

The tests cover a timeout, a `500`, an unrelated `404`, and the
explicit demotion path. An unexpected read failure must hold the
reboot and show in status. A successful retry must resume the drain,
the existing deadline must still apply, and a demoted machine must
still reboot and register again after its Node is deleted.

**Conditions this pass did not check get stamped as current.** The
status writer sets `observedGeneration` on every condition except
`RebootApproved` at the end of each pass
(`machine-operator/reconcile.go:466` to `:471`). When the Node read
failed, `NodeHealthy`, `NodeLabelsApplied`, `NodeTaintsApplied`, and
`NodeCurrent` carry forward from the previous status untouched, and
then get stamped with the current generation. A reader takes those
conditions as results of this pass.

The first fix proposed adds one condition, `NodeObserved`, that says
whether this pass read the Node. When it is False, the conditions that
depend on the Node are written as `Unknown`, with a message that names
the read error. The drain fix above reports through the same
condition. But `Unknown` would turn `Ready` False on every failed read,
and a machine whose phase changes can hold the fleet's reboot turns.
The smaller fix keeps the previous `observedGeneration` on each
condition the pass carried forward. `NodeObserved` does not exist in
the code today.

## The CRD schema, CI, and the build match the code

**The grow-only storage rule runs on status writes.** Nine CEL rules
on the root of the `Machine` schema
(`machine/manifests/machines-crd.yaml`, from line 157) compare
`spec.storage.<role>.size` against `status.boot.storage.<role>.size`. A
root rule runs on a status write as well as a spec write. A manifest
that arrived on a stick never met the API server, so it can boot a
machine whose declared size exceeds the in-cluster spec. Every status
write is then refused, and the machine goes Lost with no way out but a
spec edit.

The rules cannot move to `spec.storage`, because a rule there cannot
read `self.status`. The fix keeps the rules at the root and adds a
transition guard that passes any write that leaves `spec.storage`
unchanged, as a status write does.

**CI never fetches the flux seed.** `TestTheRealSeedParsesAndMapsWhole`
and its companion `TestTheTeardownAndItsRightsCoverTheSeed` in
`cluster-operator/flux_test.go` call `t.Skip` (lines 213 and 369) when
`seed/gotk-components.yaml` is absent. That file is a build product
that `cluster-operator/Makefile` copies from `flux/dist`, and the
checks job of `.github/workflows/component-liken.yaml` runs
`make test-go` with no step that fetches or copies it. The two tests
that exist to catch a Flux bump that breaks the seed table skip on
every CI run. They pass on a workstation because the seed happens to be
there. The fix: the checks job fetches the flux domain before the
tests, and the tests fail instead of skipping when the seed is absent
under CI.

**The release publish drills only one boot chain.** The publish job of
`.github/workflows/component-liken.yaml` runs `make release` and
`make smoke-uefi`, and it needs the checks job, whose build ran both
`make smoke-uefi` and `make smoke-bios` on the same commit. So the BIOS
chain is drilled on the development build of the commit, not on the
build stamped with the release's version. Milestone 66 proposed that
the release workflow run both drills, before that workflow was removed.
Whether the publish job must also run `make smoke-bios` is not
decided. "Open questions" below holds it.

**A killed build leaves a truncated file that Make treats as current.**
No Makefile of `liken` declares `.DELETE_ON_ERROR`, so a build killed
during `mksquashfs` or the licensing render leaves a truncated file
that is newer than its inputs, which Make then treats as current. Add
the declaration.

## The certificate workflow runs a pinned tool with a narrow token

This is supply-chain hardening of medium priority.
`.github/workflows/releases-cert.yaml` downloads
`lego_v5.2.2_linux_amd64.tar.gz` from the `go-acme/lego` GitHub
release, by version and with no digest. It extracts the binary and runs
it with `LINODE_TOKEN` set from the `RELEASES_CERT_TOKEN` secret. The
workflow and `liken.sh/terraform.tf` describe that token as scoped to
Domains and Object Storage read/write, so it can write the releases
bucket. The release upload key is scoped to that bucket alone. The
workflow uses the token for the DNS-01 challenge, and its next step
uses it again to install the bucket's TLS certificate. Nothing in the
repository limits the token to one zone. The review of 2026-09-11 did
not inspect the live credential.

HTTPS authenticates GitHub and protects the download in transit. It
does not stop someone from replacing a release asset at its origin. A
later run can then execute different code with no reviewed pin change
in this repository. A replaced `lego` asset could expose the cloud
token. A checksum fetched beside the executable at run time does not
help when an attacker can replace both files. A reviewed digest
committed here, or a signature checked against an independently trusted
key, gives an integrity check that does not come from that download.

The fix:

* Commit a reviewed SHA-256 for the `lego` archive. Verify it before
  extraction, and execute only the verified contents. A mismatch must
  fail before the step that uses the credential.
* Split `RELEASES_CERT_TOKEN` into two tokens, both declared in
  `liken.sh/terraform.tf`: one scoped to Domains, which only the `lego`
  step receives, and one scoped to Object Storage, which only the step
  that installs the certificate on the bucket receives. Each step then
  holds only the access its own work needs. The maintainer chose the
  split on 2026-10-04.
* Add a reviewable update process for the `lego` pin, as the
  `latest.sh` of every other pin gives (see "Pins and downloads" in the
  root `AGENTS.md`).

A pin fixes which upstream bytes run. It does not make them
trustworthy. The selected bytes and each later pin update still need
review. Certificate renewal keeps its schedule, and a successful run
behaves as it does now.

The tests verify that a modified archive fails before extraction and
before the token is used, and that matching bytes proceed. A rehearsal
in an authorized workflow run covers a pin update and the certificate
handshake check.

## Open questions

These need the maintainer's decision before the fix that depends on
each one is built.

1. **What a missed wait does on the restart path.** When `stopK3s`
   reaches its new deadline after SIGKILL, a k3s in uninterruptible
   sleep may still run. On the reboot path, the miss can go on to the
   reboot syscall. On the restart path, `superviseK3s` must not start a
   second k3s beside the first, and nothing decides yet what it does
   instead.
2. **Whether a failed sysfs walk keeps or withdraws the last offer.**
   This milestone's rule withholds an offer it cannot show is safe.
   The walk's new error must not delete the slice the way an empty
   list does now, and nothing decides yet whether the slice then keeps
   its last devices or drops them.
3. **Whether the release publish runs the BIOS drill.** The checks job
   drills both boot chains on the development build of the same
   commit. Running `make smoke-bios` in the publish job too drills the
   build stamped with the release's version.

## What this milestone does not do

It does not move any pin to a new version. It does not change the
CLI's credential handling or the reproducibility of the squashfs. It
does not resolve the open problems it touches beyond the fixes named
above: leader election, one-shot approvals, and fleet-wide credentials
keep their design questions.

## What the lab can measure

Every drill runs under QEMU, and none needs metal.

* A BIOS guest with a release staged: cut the power between the arm
  and the reboot, at the point `make -C dev-cluster` can stop a guest.
  The next boot must find no attempted marker for a trial that never
  ran, and must not report a false rejection.
* A guest whose `/sys/bus/pci/devices` is unreadable from the
  operator's mount namespace: the `ResourceSlice` must follow the
  decision of open question 2, and the read error must show in status.
* A guest whose API server returns 500 to the Node read for one pass
  while a reboot is approved: the reboot must hold, the drain must not
  be skipped, and the Node conditions must not claim this pass's
  generation.
* A `Machine` whose stick manifest declares a larger `podEphemeral`
  than the in-cluster spec: the status write must succeed.
* CI: the checks run must show the two seed tests as run, not skipped.

The unit tests that the operators' fixes and the certificate workflow
name run in CI, and need no drill. Both smoke drills stay green, and
every existing test stays green.
