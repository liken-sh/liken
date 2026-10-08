# 73. A staged slot stays the release it names

Milestone 73. Built 2026-10-08. It closes the open problem of
retargeting over a staged release and builds part five of
[milestone 66](../66-durability-and-safety.md#part-five-release-downloads-finish-or-stop),
the release downloads that never end. Both are faults of one writer,
the release fetcher in `machine-operator/fetch.go`, and of the slot it
writes. The QEMU drill under UEFI passed, and
[What the lab measured](#what-the-lab-measured) gives the numbers. The
BIOS boot path ran in the Go tests only.

## The problem

A machine downloads a release into its inactive slot. When the
download verifies, the operator writes a staged `SystemRelease`
record that names the release, the slot, and the digest of the
release document. `init` reads the record on the way down from any
reboot it manages and arms a one-shot trial of that slot. With
`rebootPolicy: Manual`, a record can stay staged for days.

A change of `spec.version` in that time started a download of the new
release onto the same slot. Three things went wrong:

* **The old record stayed staged.** `decideSystemStaging` returned
  early while the new download ran, and nothing withdrew the record.
* **The download replaced the slot's files one at a time.** The old
  `release.yaml` stayed until the new one replaced it at the end. A
  download that stopped halfway left the kernel of one release beside
  the system image and the document of the other.
* **`init` armed the slot without reading it.** `armProvingBoot`
  checked the running slot, the proven slot, the boot entry, and the
  fallback. It did not compare the slot's files with the record.

So any reboot that `init` manages, including a reboot for a manifest
change, could boot a slot that held two releases under the record of
one. If that boot failed, the machine recorded a rejection of a
release that never ran. If it booted, the operator compared only the
version that `init` stamps, so it could promote the mixed slot as
proven.

The download had a second fault. `fetchBytes` and `fetchArtifact`
called a bare `http.Get`. A server that accepted the request and then
sent nothing held the connection open, and the fetcher's one writer
waited forever. A changed target reset the fetcher's state, but the
old goroutine kept `busy` set, so no later download could start until
the operator restarted. `releases/fetch.go`, which downloads releases
onto a workstation, made the same two calls.

## The design

Each fault gets a fix at the place where it happens. Together they
keep one rule: a slot that the reboot path can arm holds exactly the
release its record names.

**The operator withdraws a record for another release before the
download starts.** `convergeSystemRelease` compares the staged record
with the ask: version, slot, and digest. A record for anything else
is withdrawn before `Ensure` runs. A withdrawal that fails stops the
pass with `StagingFailed`, and no download starts, so the slot keeps
the release that the record names. A record for the same ask stays,
because the slot already holds that release, or the download repairs
it in place.

**The fetcher removes the slot's document before it writes.** When the
slot's `release.yaml` is not the document of the release being
fetched, `withdrawSlotDocument` removes it and flushes the slot before
the first artifact is written. A slot with a document is a slot whose
artifacts were complete when that document was written, and that no
writer has changed since. On FAT, an fsync of the directory does not
write the directory entry, so the flush calls `syncfs` first, the
same pair that `flushSlot` in `init/slotloader.go` uses.

**`init` checks the slot before it arms.** `checkStagedSlot` reads the
slot's `release.yaml`, compares its sha256 with the record's digest,
and verifies every artifact against the document. A slot that fails
is not armed, and the console names the cause. The check reads a few
hundred megabytes, which costs seconds on the way down, once for each
trial. This check alone stops the mixed boot. The two fixes above
keep the state that it refuses from arising.

**The reboot path arms after every process has ended.** The firmware
turn in `rebootMachine` ran before `killEverything`, while the
operator's container could still write the slot. It now runs after,
so no write can land between the check and the boot it arms. The
filesystems are still mounted at that point, and the actuators run no
program, so nothing in the turn needs a process that the kill ended.

**A retarget stops the old download first.** `Ensure` cancels the
running download when the ask changes, with the cause
`errSuperseded`. The new ask waits, and its condition says so, until
the old goroutine returns. The next pass starts it. One writer
changes the slot at a time. The cancelled download removes its
`.partial` file through the path that every failed write takes, and a
cancellation does not count in `liken_release_download_failures_total`.

**A download stops when it stalls.** `releases.Get` cancels a request
when no byte arrives for `StallLimit`, one minute, from the request
to the headers and between any two reads of the body. Both fetchers
use it. It bounds progress and not the whole transfer, so a slow link
that keeps delivering finishes, and a stalled one fails as a
transient error that the next pass retries.

## What did not change

The API, the conditions other than the new use of `StagingFailed`,
the reboot policy, manual approval, and fallback behave as before. A
retarget is not refused, and an approval authorizes what it authorized
before. The timeout is a constant, not a setting, because the fix
needs none.

## Tests

The operator's fetch tests run in `synctest` bubbles against
`kubernetes/apiservertest`, so a minute of silence costs no real time.
They show that a retarget cancels the old download and that at most
one download is in flight, that the slot carries no document while it
holds the files of two releases, and that a stalled download fails
after the stall limit and verifies on the retry. `retarget_test.go`
shows that a record for another release is withdrawn before the first
request, that a record for the same ask stays, and that a withdrawal
that fails sends no request. In `init`, `stagedslot_test.go` arms a
slot that holds the staged release and refuses a slot with another
release's document, a slot with another release's kernel, and a slot
with no document. `releases/download_test.go` covers a stall before
the headers and in the body, a slow transfer that makes progress, a
cancellation by the caller, and a refusal by the server. Each new
behavior was removed once, and its test failed.

## What the lab measured

The drill ran on 2026-10-08 on the dev cluster's `node-1`, alone,
under OVMF, with `rebootPolicy: Manual`. Two local releases,
`2026.10.08-901` and `2026.10.08-902`, came from `make release`, and
`liken serve` served them.

* **A retarget withdraws the staged release.** With 901 staged on
  slot B, the target moved to 902 while 902's `boot.cpio` was missing
  from the channel. The operator logged the withdrawal of 901's record
  before its first request for 902. The download wrote 902's
  `liken.sqfs` over 901's and failed on the `404`, so slot B held
  files of both releases, with no document and no staged record.
* **A stalled server fails the download.** With `liken serve` stopped
  by `SIGSTOP`, the condition read `no bytes arrived for 1m0s` 72
  seconds later. After `SIGCONT`, the retry fetched only the artifacts
  that did not verify in place, and 902 staged.
* **An ordinary upgrade still works.** The approved reboot logged
  `stopping every remaining process` at 1866.04 s and armed the trial
  at 1871.62 s: the 5-second grace period, then about half a second to
  verify roughly 500 MB on the slot. The trial booted slot B and the
  operator promoted 902.
* **`init` refuses a slot that does not match its record.** With 901
  staged on slot A, seven bytes were appended to slot A's `boot.cpio`
  from a debug pod. The approved reboot logged `slot A does not hold
  release 2026.10.08-901 as staged: boot.cpio does not match the
  release document: boot.cpio is 13318151 bytes, want 13318144` and
  booted slot B again. On that boot the operator downloaded only
  `boot.cpio`, the standing approval requested the reboot again, the
  trial armed, and 901 was promoted on slot A.

The drill did not reboot node-1 while the download of 902 was failing.
The cluster operator counts a machine in the phase `Updating` as
unavailable (`available` in `cluster-operator/rollout.go`), so the
requested reboot waited for a turn that the conductor did not grant.
The arming check that such a reboot would meet is the one the last
step exercised. The BIOS actuator ran in the Go tests only.
