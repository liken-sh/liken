# 94. Durability and safety fixes that are not built

Milestone 94. Proposed 2026-11-10. It holds every fix of
[milestone 66](66-durability-and-safety.md) that was not
built when that milestone closed. A survey of the tree on 2026-09-11
found a set of places where `liken`'s own durability and safety rules
do not hold under a power cut, a failed read, or a build that stopped
early. Milestone 66 holds that survey, the dated corrections of
2026-10-10, and the evidence of each fix that it built.

Status on 2026-11-17: closed. One fix is built: every Makefile of the
repository declares `.DELETE_ON_ERROR` (b5a8ad03), and the section on
it below records how. Every other fix is a separate small bug with its
own evidence, so each one continues as its own open problem, with its
evidence, its fix, its design question if it has one, and the drill
that proves it:

* [The BIOS arm has no device flush](../open-problems/the-bios-arm-has-no-device-flush.md),
  with the readback after the write that reads the page cache.
* Two comments state opposite rules about FAT. Fixed on 2026-11-19:
  the comment of `syncDirectory` states the rule that the lab showed,
  and `installGRUB` flushes the boot home with `flushSlot`.
* The primary and backup GPT have no barrier between them. Fixed on
  2026-11-19: a table write writes the backup copy and syncs it, and
  then writes the primary copy and syncs it.
* [`LastUsableLBA` is off by one](../open-problems/last-usable-lba-is-off-by-one.md).
* The wpa_supplicant control channel can panic on close. Fixed on
  2026-11-17: the reader sends each event under the lock that `close`
  holds, and stops when the control is closed.
* [Two waits have no bound after SIGKILL](../open-problems/two-waits-have-no-bound-after-sigkill.md),
  with open question 1.
* [The proving watchdog blocks its own shutdown](../open-problems/the-proving-watchdog-blocks-its-own-shutdown.md).
* The node password is minted after a step that can fail. Fixed on
  2026-11-19: init accepts a link that already names the password
  directory, and fails the boot when it cannot make the link.
* [An empty device walk deletes the `ResourceSlice`](../open-problems/an-empty-device-walk-deletes-the-resourceslice.md),
  with open question 2.
* [A reboot skips the drain on any failed Node read](../open-problems/a-reboot-skips-the-drain-on-any-failed-node-read.md).
* Conditions this pass did not check are stamped as current. Fixed on
  2026-11-17 in the smaller form: a pass that did not read the Node
  leaves the `observedGeneration` of the four Node conditions as it was.
* [The grow-only storage rule runs on status writes](../open-problems/the-grow-only-storage-rule-runs-on-status-writes.md).
* CI never fetches the flux seed. Fixed on 2026-11-17: `make test-go`
  copies the seed before the tests, and the two seed tests fail under
  CI when it is absent.
* [The release publish drills only one boot chain](../open-problems/the-release-publish-drills-only-one-boot-chain.md),
  with open question 3.
* [The certificate workflow runs an unpinned tool with a wide token](../open-problems/the-certificate-workflow-runs-an-unpinned-tool-with-a-wide-token.md).

## What is built

Milestone 66 built the orphan reaper's bound, a deadline on every
request of the operators' API client, the release downloads that
finish or stop (in
[milestone 73](73-a-staged-slot-stays-the-release-it-names.md)),
the refusal to offer disks when a machine's facts do not read, the full
`status.boot.network` schema, the pipe leak, the crash batch, the one
writer of the k3s log, the two smaller cases of part three, the
condition roll-up, the commit pin of every GitHub Action, and `mke2fs`
in the image's list of programs. Its status paragraph of 2026-11-10
names the commits.

## The rule this milestone enforces

Every fix of this milestone follows one rule. When the code cannot
confirm a state, it must refuse or report, and never act. So a trial
whose arm write may not have reached the disk does not count as armed,
a failed read does not count as an empty result, and a condition that
this pass did not check is not stamped as current again.

## The build matches the code

**A killed build leaves a truncated file that Make treats as current.**
No Makefile of `liken` declares `.DELETE_ON_ERROR`, so a build killed
during `mksquashfs` or the licensing render leaves a truncated file
that is newer than its inputs, which Make then treats as current. Add
the declaration.

Built on 2026-11-17: every Makefile of the repository declares
`.DELETE_ON_ERROR`, and a test in `ci/makefiles_test.go` fails on one
that does not. Two Makefiles are excepted, `trust/Makefile` and
`toolchains/manifest/Makefile`, because each is a part of a pinned
component's recipe. Each takes the declaration with the next revision
of those components.

## What this milestone does not do

It does not move any pin to a new version. It does not change the
CLI's credential handling or the reproducibility of the squashfs. It
does not resolve the open problems it touches beyond the fixes named
above: leader election, one-shot approvals, and fleet-wide credentials
keep their design questions.

## What the lab can measure

Each open problem above holds the drill that proves its fix. Every
drill runs under QEMU, and none needs metal. The unit tests that the
operators' fixes and the certificate workflow name run in CI, and need
no drill. Both smoke drills stay green, and every existing test stays
green.
