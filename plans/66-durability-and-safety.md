# Durability and safety in the boot chain, the operators, and CI

Milestone 66. Proposed. A survey of the tree on 2026-09-11 found a set
of places where `liken`'s own promises do not hold under a power cut, a
failed read, or a build that stopped early. None of them has failed in
the lab yet. Each one is small. This milestone fixes them as one
round, because the firmware work in plan 33 stands on exactly these
paths, and a firmware trial must not be the first thing to find them.

The round has four parts: the boot chain's writes, init's process
supervision, the operators' failure paths, and the CRD and CI
contracts. Dependency bumps are not part of it. The stale pins the
survey listed move on their own schedule.

## The rule this milestone enforces

Every fix below follows one rule. When the code does not know, it must
refuse or report, never act. A write that may not have reached the
disk is not armed. A read that failed is not an empty result. A
condition that this pass did not check is not restamped as current.

## Part one: the boot chain's writes reach the disk

**The BIOS arm has no device flush.** `armTrial` and `assertProven` in
`init/grubactuator.go` write `try_slot` and `default_slot` through
`writeFileDurably` (`init/durable.go`), which fsyncs the temp file and
renames it. Nothing flushes the block device after the rename. On FAT,
the rename's directory entry is in buffers attached to the block
device, and an fsync of the directory does not reach them.
`init/slotloader.go` explains this at length, and the lab found it on
the first promotion drill of plan 33's built half. The GRUB arm takes
the same path with no flush. A power cut after the arm can lose the
arm while the attempted marker stands, and the next boot reads that as
a release that ran and fell back. The `grub.cfg` heal in the same
file has the same gap, and a boot home with a `.partial` file and no
`grub.cfg` drops GRUB to a rescue prompt.

The fix is `unix.Sync()` after each of those writes, which is what the
installer, the loader writer, and the report already do.

**The readback after that write proves nothing.** The same file
re-reads what it just wrote and calls that the same trust as the UEFI
dialect's readback of `BootOrder`. The UEFI readback crosses efivarfs
to the firmware. This one comes back from the page cache. The flush
above is what makes the readback mean something.

**Two comments state opposite rules about FAT.** `init/durable.go`
says a FAT directory fsync flushes the whole block device, so one call
covers every rename. `init/slotloader.go` says the opposite, and the
lab proved the second one. `init/install.go` acts on the first one at
its `grub.cfg` write and calls only `syncDirectory`. The wrong comment
is the reassuring one, so it keeps producing new call sites without a
flush. This milestone deletes it and makes every FAT writer call
`unix.Sync()`.

**A half-copied crash batch is read as complete.** `preserveCrashRecords`
(`init/crash.go`) returns at once when the destination directory
exists, and it creates that directory before it writes any file. A
machine that dies during the copy leaves a partial directory. The next
boot takes the "already safe" branch and then clears pstore, which
erases the only remaining copy. The fix copies into a `.partial`
directory and renames it when every file is written.

**The primary and backup GPT have no barrier between them.**
`WriteTableInPlace` (`disks/gpt.go`) writes all five chunks of both
tables and calls `Sync` once at the end. A power cut or a drive that
reorders writes can leave the primary entries half-written while the
backup is already overwritten, and then `ReadGPT` reports neither copy
readable. The fix is one `Sync` after the primary table and one after
the backup, so that at every moment one complete table exists.

**`LastUsableLBA` is off by one.** `disks/gpt.go` says the last usable
sector is 34 sectors from the end and returns the one 35 from the end.
The test pins the wrong value. This is conservative and loses one
sector, and it makes `liken`'s tables disagree with every other
partitioner. Fix the function and the test together.

## Part two: init supervises processes safely

**Exit statuses of orphans are kept forever.** init is PID 1, so it
reaps every orphan on the machine. `deathRegistry.record`
(`init/supervisor.go`) parks each unmatched exit status in a map, and
only `await` removes an entry. Almost no orphan has a waiter, so the
map grows for the life of the boot. After the kernel's pid counter
wraps, `await` on a new process can return an old process's status at
once. `superviseK3s` would then restart a k3s that is still running.
The fix is a bound: an entry that no waiter claims within a short
window is dropped.

**Two goroutines write one unlocked log file.** `superviseK3s` builds
two separate `io.MultiWriter` values for stdout and stderr, both
ending in one `*cappedLogFile`. `os/exec` starts one copier goroutine
for each, and `cappedLogFile.Write` has no lock over its size, its
line state, or the file it rotates. A rotate can close the file under
the other writer. A runtime panic in PID 1 is a kernel panic. The fix
is one writer for both streams, or a mutex in `cappedLogFile`.

**The wpa_supplicant control channel can panic on close.**
`wpaControl.close` (`init/wpactrl.go`) closes the event channel under
the lock, and the read goroutine sends on that channel without the
lock. Closing the socket first does not stop a goroutine that is
already past `Read`. A send on a closed channel panics. The fix is a
done channel the reader checks, or a close that waits for the reader.

**Two waits have no bound after SIGKILL.** `stopK3s` receives on the
death channel with no timeout after the kill. On the reboot path this
is between the operator's request and the reboot syscall.
`stopSupplicant` (`init/wireless.go`) has the same receive, and on the
plane-cancel path the reaper has already returned, so nothing will
ever send. Both receives get a deadline, and a miss is printed and
reported.

**The proving watchdog blocks its own shutdown.** `provingWatch`
(`init/proving.go`) calls `rebootMachine` from inside a machine-plane
component. `rebootMachine` shuts the plane down and waits on the
plane's wait group, which counts the goroutine that called it. Every
watchdog reboot burns the full ten-second shutdown timeout and then
prints that the proving watch did not stop. The watch should signal
the reboot and return, so the plane can stop it like any other
component. `provingWatch` has no test today and gets one here.

**`run` leaks a pipe per call.** `run` (`init/supervisor.go`) takes
`StdoutPipe` and never calls `Wait`, so the parent's end of the pipe
is never closed. `reportWhenReady` calls it every few seconds for up
to ten minutes. One `Close` after `ReadAll` fixes it.

**The node password is minted after a step that can fail.**
`persistNodePassword` (`init/k3s.go`) returns early when the symlink
write fails, and `mintNodePassword` never runs. That mint exists to
stop k3s from writing its own password with a torn-write window that
locks a machine out of its cluster. The mint is the point and the
symlink is incidental, so the mint runs first.

## Part three: the operators refuse instead of guessing

**A facts-read error offers the machine's own disks.**
`reconcile.go` publishes the device inventory whenever the Node read
succeeded, with `facts` nil when the facts read failed.
`platformBlocks` (`machine-operator/dra.go`) then protects nothing,
and a claim can allocate the machine's system disk. This is the open
problem [missing-facts-expose-system-disks](open-problems/missing-facts-expose-system-disks.md).
The fix is the one that document proposes: with no facts, publish no
inventory, and say so in status.

**An empty device walk deletes the ResourceSlice.**
`EnsureResourceSlice` (`kubernetes/resourceslices.go`) deletes the
slice when the device list is empty, and `DiscoverDevices`
(`hardware/sysfs.go`) turns an unreadable `/sys` directory into an
empty list. A machine with no devices and a machine whose sysfs could
not be read are the same input. The fix is a type change:
`DiscoverDevices` returns an error, and the slice writer refuses to
delete on an error.

**The drain bypass is wider than its reason.** `disruptions.gate`
(`machine-operator/reconcile.go`) skips the drain whenever the Node
read failed. The comment justifies that by demotion, when there is no
Node to cordon. A 500 or a timeout on one pass has the same effect: an
approved reboot skips eviction. No test covers the gate. This is the
open problem [node-read-errors-bypass-draining](open-problems/node-read-errors-bypass-draining.md).
The fix distinguishes the two: a 404 during a demotion skips the
drain, and any other error holds the reboot and reports why.

**Conditions this pass did not check get stamped as current.** The
status writer sets `observedGeneration` on every condition at the end
of each pass. When the Node read failed, `NodeHealthy`,
`NodeLabelsApplied`, and `NodeTaintsApplied` carry forward from the
previous status untouched, and then get stamped with the current
generation. A reader takes that as a judgment this pass made. The fix
adds one condition, `NodeObserved`, that says whether this pass read
the Node. When it is False, the three conditions that depend on the
Node are written as `Unknown`, with a message that names the read
error. The drain fix above reports through the same condition.

**No request has a deadline.** `kubernetes/apiclient.go` sets a dial
timeout, a response-header timeout, and an idle timeout, and no
timeout on the client or the body read. A server that stalls mid-body
hangs the reconcile pass, and the heartbeat with it. The comment says
every wait ends inside the forty-second heartbeat window, and the body
read is not covered. The fix threads one `context.Context` per pass,
with a deadline under `HeartbeatRenewAfter`, through every request.
This is also the cancellation that
[release-downloads-can-block-upgrades](open-problems/release-downloads-can-block-upgrades.md)
asks for, and the two bare `http.Get` calls in `fetch.go` move to the
same client.

**Two smaller cases of the same rule.** `daemonSetVersion`
(`cluster-operator/steward.go`) returns an empty string on any error,
and the rollout gate reads an empty string as "no version applied",
which turns the leader-first gate off for that sweep. The flux janitor
(`cluster-operator/janitor.go`) sets `deleted` only on success and
falls through to stripping finalizers when a delete failed for a
reason other than not-found. Both return the error instead.

## Part four: the CRD and CI contracts match the code

**`status.boot.network` prunes half the record.** The Go type is
`*NetworkSpec`, which carries `hostEntries` and each interface's
`wireless` block. The CRD schema declares only `interfaces` with four
fields. The API server prunes the rest. Drift detection works only
because the operator reads the facts tree and never the API object.
An operator reading `kubectl get machine -o yaml` cannot see the
wireless network the boot actuated, and the generated manual page
cannot document it. The schema gets the full type.

**A duplicate modalias rejects the whole status write.**
`status.hardware.unclaimed` is a map-typed list keyed on `modalias`,
and `hardware/unclaimed.go` never deduplicates. Two identical undriven
USB dongles produce two entries with one key, the API server refuses
the write, and the machine publishes no status at all. The fix folds
duplicates into one entry with a count.

**The grow-only storage rule fires on status writes.** Nine CEL rules
on the root of the Machine schema compare `spec.storage.<role>.size`
against `status.boot.storage.<role>.size`. A root rule runs on a
status write as well as a spec write. A manifest that arrived on a
stick never met the API server, so it can boot a machine whose
declared size exceeds the in-cluster spec. Every status write is then
refused, and the machine goes Lost with no way out but a spec edit.
The rules move to `spec.storage`, where only a spec write triggers
them.

**The condition roll-up omits four conditions the code sets.** The
`conditions` description in the schema lists the conditions by name,
and the manual's reference page generates from it. `HostEntriesApplied`,
`NodeTaintsApplied`, `ModuleParametersApplied`, and
`RebootRequestHonored` are set by the operator and absent from that
list. `NodeObserved` from part three joins them. The list gets every
condition the code writes, and a test compares the two.

**CI never fetches the flux seed.** `TestTheRealSeedParsesAndMapsWhole`
and its companion in `cluster-operator/flux_test.go` skip when
`seed/gotk-components.yaml` is absent. That file is a build product
copied from `flux/dist`, and the checks workflow runs `make test-go`
with no step that fetches or copies it. The two tests that exist to
catch a Flux bump that breaks the seed table skip on every CI run.
They pass on a workstation because the seed happens to be there. The
checks workflow fetches the flux domain before the tests, and the
tests fail instead of skipping when the seed is absent under CI.

**No workflow sets pipefail.** GitHub's default shell for a `run` step
is `bash -e` without `pipefail`. The release workflow pipes `s3cmd ls`
through `awk` and `sed` into the file that `liken index` reads. A
failed listing writes an empty file, and the workflow publishes an
index and a `versions.yaml` that name no releases. Two other steps
pipe `gh run list` and `curl` the same way. One `defaults.run.shell:
bash` at the top of each workflow turns pipefail on for every step.

**The release proves only one boot chain.** The build workflow runs
`make smoke-uefi` and `make smoke-bios`. The release workflow runs
only `make smoke-uefi`, and it requires a green checks run for the
commit, not a green build run. A commit whose BIOS drill failed can
be tagged and published, and releases are immutable. The release
workflow runs both drills.

**The certificate tool is unverified and holds the wider token.** The
certificate workflow downloads `lego` by version with no digest and
runs it with the account-wide Linode token, which can write the
releases bucket. The release upload key is scoped to that bucket
alone. This is the open problem [ci-executables-need-immutable-pins](open-problems/ci-executables-need-immutable-pins.md).
This milestone commits a SHA-256 for the archive and verifies it
before extraction, and narrows the token to DNS.

**Two Make gaps ship stale files.** `image/Makefile` lists the
programs the image copies from each vendored domain, and the comment
says the list mirrors the root Makefile's. It omits `mke2fs`, so an
incremental `make release` after an e2fsprogs change ships the old
binary. No Makefile declares `.DELETE_ON_ERROR`, so a build killed
during `mksquashfs` or the licensing render leaves a truncated file
that is newer than its inputs, which Make then treats as current. Add
the prerequisite and the declaration.

## What this milestone does not do

It does not move any pin. It does not change the CLI's credential
handling, the four fetch scripts that take their checksum from the
same origin, or the reproducibility of the squashfs. Those are real
and they are separate rounds. It does not resolve the open problems it
touches beyond the fixes named above: leader election, one-shot
approvals, and fleet-wide credentials keep their design questions.

## What the lab can measure

Every drill runs under QEMU, and none needs metal.

* A BIOS guest with a release staged: cut the power between the arm
  and the reboot, at the point `make -C dev-cluster` can stop a guest.
  The next boot must find no attempted marker for a trial that never
  ran, and must not report a false rejection.
* A guest with a crash record: kill the guest during the pstore copy.
  The next boot must find the `.partial` directory, copy again, and
  clear pstore only after the copy completes.
* A guest whose `/sys/bus/pci/devices` is unreadable from the
  operator's mount namespace: the ResourceSlice must stay as it was,
  and `NodeObserved` or its equivalent must name the read error.
* A guest whose API server returns 500 to the Node read for one pass
  while a reboot is approved: the reboot must hold, the drain must not
  be skipped, and the three Node conditions must read `Unknown`.
* A Machine whose stick manifest declares a larger `podEphemeral` than
  the in-cluster spec: the status write must succeed.
* Two identical undriven USB devices on one guest: the status write
  must succeed and `unclaimed` must hold one entry.
* CI: the checks run must show the two seed tests as run, not skipped.
  A release run must show both smoke drills.

Both smoke drills stay green, and every existing test stays green.
