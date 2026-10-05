# Durability and safety in the boot chain, the operators, and CI

Milestone 66. Proposed, and partly built. A survey of the tree on
2026-09-11 found a set of places where `liken`'s own durability and
safety rules do not hold under a power cut, a failed read, or a build
that stopped early. None of them has failed in the lab yet. Each one is
small. This milestone fixes them as one round, because the firmware
work in plan 33 depends on exactly these code paths, and a firmware
trial must not be the first test that finds these defects.

This is the one plan for durability and safety in the OS and its CI.
It holds four open problems in full, with their evidence, safeguards,
and tests: system disks offered when the facts are missing, and the
drain skipped on a failed `Node` read (part three), release downloads
with no bound (part five), and CI executables with no immutable pin
(part six). Those four documents are deleted.

Status on 2026-10-04:

* Built: init no longer keeps the exit status of every orphan
  (cadb76f2). The reaper parks a status only for a child that init
  started and has not yet awaited.
* Built: every request of the operators' API client has a deadline
  (3c594a1c, be5dcace, d57451fa). The exception is the release
  fetchers: `machine-operator/fetch.go` and `releases/fetch.go` still
  call a bare `http.Get`. Part five covers them.
* No longer applies: the pipefail item in part four. The release
  workflow it names was removed (7faa174e). The bucket listing moved
  into `releases/publish.sh` (ab96344a), which sets `pipefail`.
* Every other item is not built.

The round has six parts: the boot chain's writes, init's process
supervision, the operators' failure paths, the CRD schema and CI
workflows, the release downloads, and the pins on CI executables.
Dependency bumps are not part of it. The stale pins the survey listed
move on their own schedule.

## The rule this milestone enforces

Every fix below follows one rule. When the code cannot confirm a
state, it must refuse or report, and never act. So a trial whose arm
write may not have reached the disk does not count as armed, a failed
read does not count as an empty result, and a condition that this
pass did not check is not stamped as current again.

## Part one: the boot chain's writes reach the disk

**The BIOS arm has no device flush.** `armTrial` and `assertProven` in
`init/grubactuator.go` write `try_slot` and `default_slot` through
`writeFileDurably` (`init/durable.go`), which fsyncs the temp file and
renames it. Nothing flushes the block device after the rename. On FAT,
the rename's directory entry is in buffers attached to the block
device, and an fsync of the directory does not reach them.
`init/slotloader.go` explains this at length, and the lab found it on
the first promotion drill of the built part of plan 33. The GRUB arm
takes the same path with no flush. A power cut after the arm can lose
the arm write while the attempted marker remains, and the next boot reads that as
a release that ran and fell back. The `grub.cfg` heal in the same
file has the same gap, and a boot home with a `.partial` file and no
`grub.cfg` drops GRUB to a rescue prompt.

The fix is `unix.Sync()` after each of those writes, which is what the
installer, the loader writer, and the report already do.

**The readback after that write reads the page cache.** The same file
re-reads what it just wrote, and its comment says this check is as
reliable as the UEFI dialect's readback of `BootOrder`. The UEFI
readback goes through efivarfs to the firmware. This one comes back
from the page cache. The readback is useful only after the flush
above.

**Two comments state opposite rules about FAT.** `init/durable.go`
says a FAT directory fsync flushes the whole block device, so one call
covers every rename. `init/slotloader.go` says the opposite, and the
lab showed that the second one is correct. `init/install.go` follows
the first one at its `grub.cfg` write and calls only `syncDirectory`.
The wrong comment says one directory fsync is enough, so code that
follows it keeps adding new call sites without a flush. This milestone deletes it and makes every FAT writer call
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

**Exit statuses of orphans are kept forever.** Built in cadb76f2. init is PID 1, so it
reaps every orphan on the machine. `deathRegistry.record`
(`init/supervisor.go`) stores each unmatched exit status in a map, and
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
`stopSupplicant` (`init/wireless.go`) has the same receive. When the
machine plane is cancelled, the reaper has already returned, so
nothing will ever send. Both receives get a deadline, and a miss is printed and
reported.

**The proving watchdog blocks its own shutdown.** `provingWatch`
(`init/proving.go`) calls `rebootMachine` from inside a machine-plane
component. `rebootMachine` shuts the plane down and waits on the
plane's wait group, which counts the goroutine that called it. Every
watchdog reboot waits the full ten-second shutdown timeout and then
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
locks a machine out of its cluster. The password matters more than
the symlink, so the fix runs `mintNodePassword` first.

## Part three: the operators refuse instead of guessing

**A facts-read error publishes the machine's own disks as devices.**
This is a bug of high priority. `reconcile` (`machine-operator/reconcile.go`)
reads `factsTree.Read()`. An error leaves `facts` nil and sets
`FactsPublished` to False. The same pass still calls
`publishDeviceInventory` when the Node read succeeded.
`platformBlocks(nil)` (`machine-operator/dra.go`) then returns an empty
protection set, and `inventoryDevices` applies no storage-role
exclusions. A driven disk with deliverable device nodes can appear in
the `ResourceSlice` even when it holds system or state partitions, and
a claim can allocate it.

Claim preparation does not check protection again.
`prepareClaim` (`machine-operator/draplugin.go`) resolves the
allocation against current sysfs data and writes the device nodes into
a CDI spec. `refreshCDISpecs` (`machine-operator/cdi.go`) rewrites
those specs. Neither one checks whether the nodes back a storage role.
So the API reports the facts failure, and the device-access path still
delivers the device.

An attack needs two things: facts that cannot be read, and a workload
that is authorized to claim a matching `DeviceClass`. Raw disk access
could expose stored credentials or let the workload corrupt host
storage. The finding does not show that an ordinary pod can cause the
facts failure, or claim a disk without permission to claim it.

A temporary fixture with a fake sysfs and API reproduced the path. With
a protection set of `sda: true`, the inventory excluded the disk. With
missing facts, the same inventory functions offered it, and preparation
wrote `/dev/sda` into the CDI spec. The fixture used no live cluster and
no physical device. The device reference
(`docs/content/docs/reference/devices.md`) states the intended
exclusion: system disks are never delivered to workloads.

The fix makes that statement true again:

* Treat storage protection as unknown until the operator knows that
  its protection set is complete. A non-nil facts object, or a readable
  subset of roles, does not show that the remaining disks are safe to
  offer.
* With no facts, publish no inventory, and say so in status. Skipping
  publication alone is not enough, because an unsafe earlier offer can
  stay active, so the operator withdraws the node's inventory. In
  general, the operator withholds each offer that it cannot show is
  safe.
* Check protection again in `prepareClaim` and in the CDI refresh.
  Publication can lag, API writes can fail, and an allocation may
  already exist. Unknown protection must not become permission to
  deliver a device.
* Keep a memory-backed machine distinct from a failed read. A complete
  record with no disk-backed roles is different from an incomplete
  record whose exclusions are unknown.

A possible improvement reads the storage facts apart from the other
facts. That is safe only if the read produces the complete
storage-protection set. Keeping only the roles it could read is not
safe. The fix needs no new `DeviceClass` and no new claim format. A
facts failure may prevent new device use until protection is known.
That refusal is better than raw access to an unidentified disk.

The tests cover unreadable facts, incomplete facts, a complete
memory-backed machine, and a previously published unsafe slice.
Preparation and refresh must refuse a protected or uncertain device
even after an allocation succeeded. One test makes the API fail while
the operator withdraws the offer: the local delivery checks must still
refuse the device when publication cannot be repaired.

**An empty device walk deletes the ResourceSlice.**
`EnsureResourceSlice` (`kubernetes/resourceslices.go`) deletes the
slice when the device list is empty, and `DiscoverDevices`
(`hardware/sysfs.go`) turns an unreadable `/sys` directory into an
empty list. A machine with no devices and a machine whose sysfs could
not be read are the same input. The fix is a type change:
`DiscoverDevices` returns an error, and the slice writer refuses to
delete on an error.

**The drain is skipped on any failed Node read.** This is a bug of
medium priority. `disruptions.gate` (`machine-operator/reconcile.go`)
calls `gateThroughDrain` only when a reboot is requested, the conductor
granted a turn, and the Node read succeeded. On a timeout, a server
error, or any other read failure, it leaves `requestReboot` set. The
comment gives demotion as the reason, when there is no Node to cordon.
The condition does not distinguish that lifecycle state from an API
error on a node that still runs workloads.

A 500 or a timeout on one pass has the same effect: an approved reboot
skips eviction. The machine then follows its shutdown sequence without
the Eviction API, so no `PodDisruptionBudget` protects its workloads.
The convergence still reports the normal reboot-requested result, and
it reports no drain-read failure. No test covers the gate.

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
(`machine-operator/demotion.go`) writes its reboot intent before it
deletes the Node, because the deletion can terminate its own pod. That
intent uses the runtime channel on tmpfs, so it does not survive a
power loss. The fix must not strand a machine whose Node was removed
on purpose.

The fix needs no new user-facing disruption policy. The five-minute
`drainDeadline` allows a reboot on purpose when workloads do not leave
in time. The fix does not change that limit, `rebootPolicy`, or the
conductor's grant rules. Whether a `PodDisruptionBudget` should block
a reboot with no limit is a separate policy question.

The tests cover a timeout, a `500`, an unrelated `404`, and the
explicit demotion path. An unexpected read failure must hold the
reboot and show in status. A successful retry must resume the drain,
the existing deadline must still apply, and a demoted machine must
still reboot and register again after its Node is deleted.

**Conditions this pass did not check get stamped as current.** The
status writer sets `observedGeneration` on every condition at the end
of each pass. When the Node read failed, `NodeHealthy`,
`NodeLabelsApplied`, and `NodeTaintsApplied` carry forward from the
previous status untouched, and then get stamped with the current
generation. A reader takes those conditions as results of this pass. The fix
adds one condition, `NodeObserved`, that says whether this pass read
the Node. When it is False, the three conditions that depend on the
Node are written as `Unknown`, with a message that names the read
error. The drain fix above reports through the same condition.

**No request has a deadline.** Built in 3c594a1c, be5dcace, and
d57451fa, as a fifteen-second limit on each request rather than a
context per pass. The release fetchers remain, in part five.
`kubernetes/apiclient.go` sets a dial
timeout, a response-header timeout, and an idle timeout, and no
timeout on the client or the body read. A server that stalls mid-body
hangs the reconcile pass, and the heartbeat with it. The comment says
every wait ends inside the forty-second heartbeat window, and the body
read is not covered. The fix threads one `context.Context` per pass,
with a deadline under `HeartbeatRenewAfter`, through every request.
This is also the cancellation that the release downloads in part five
need, and the two bare `http.Get` calls in `fetch.go` move to the same
client.

**Two smaller cases of the same rule.** `daemonSetVersion`
(`cluster-operator/steward.go`) returns an empty string on any error,
and the rollout gate reads an empty string as "no version applied",
which turns the leader-first gate off for that sweep. The flux janitor
(`cluster-operator/janitor.go`) sets `deleted` only on success and
falls through to stripping finalizers when a delete failed for a
reason other than not-found. Both return the error instead.

## Part four: the CRD schema and CI workflows match the code

**The API server prunes part of `status.boot.network`.** The Go type is
`*NetworkSpec`, which contains `hostEntries` and each interface's
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

**The grow-only storage rule runs on status writes.** Nine CEL rules
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

**No workflow sets pipefail.** No longer applies: see the status
above. GitHub's default shell for a `run` step
is `bash -e` without `pipefail`. The release workflow pipes `s3cmd ls`
through `awk` and `sed` into the file that `liken index` reads. A
failed listing writes an empty file, and the workflow publishes an
index and a `versions.yaml` that name no releases. Two other steps
pipe `gh run list` and `curl` the same way. One `defaults.run.shell:
bash` at the top of each workflow turns pipefail on for every step.

**The release workflow tests only one boot chain.** The build workflow runs
`make smoke-uefi` and `make smoke-bios`. The release workflow runs
only `make smoke-uefi`, and it requires a green checks run for the
commit, not a green build run. A commit whose BIOS drill failed can
be tagged and published, and releases are immutable. The release
workflow runs both drills.

**Two Make gaps ship stale files.** `image/Makefile` lists the
programs the image copies from each vendored domain, and the comment
says the list mirrors the root Makefile's. It omits `mke2fs`, so an
incremental `make release` after an e2fsprogs change ships the old
binary. No Makefile declares `.DELETE_ON_ERROR`, so a build killed
during `mksquashfs` or the licensing render leaves a truncated file
that is newer than its inputs, which Make then treats as current. Add
the prerequisite and the declaration.

## Part five: release downloads finish or stop

This is a bug of medium priority. The release fetcher in
`machine-operator/fetch.go` has one writer. A stalled HTTP download can
hold that writer indefinitely. A change to the release target or source
does not cancel the request, so later upgrades cannot start.

**The download has no deadline.** `fetchBytes` and `fetchArtifact`
call `http.Get` with no deadline for the whole request and no
cancellation context. The default transport has some connection
timeouts, but it does not bound the whole response. A server can stop
sending headers or body bytes and keep the connection open. The
document and artifact readers limit the number of bytes they consume.
Those limits stop an oversized download from consuming unlimited
memory or slot space. They do not limit the time spent waiting for
bytes. `releases/fetch.go` makes the same two bare calls.

**An obsolete download keeps `busy` set.** `Ensure` permits one active
download. A changed request resets its snapshot to `Idle`, but `busy`
stays true until the old goroutine returns. `run` then discards the
obsolete result, but no code cancels that goroutine. A stalled request
can therefore block a new version or a corrected source URL
indefinitely. A retarget does not always cause an indefinite stall. If
the old download finishes, a later reconcile pass can start the new
one. Even then, the missing cancellation causes unnecessary waiting
and writes.

**The stall shows only as a long update.** The download runs apart
from the reconcile pass, so this fault does not stop the heartbeat.
The conditions show it: `versionCondition`
(`machine-operator/release.go`) reports `VersionConverged=False` with
reason `Downloading`, which `machine-operator/phase.go` maps to
`Updating`. That state has no time limit, and nothing recovers
automatically from a permanently stalled request. A restart of the
operator ends the stuck request, but an upgrade must not depend on a
person who restarts it.

A temporary local HTTP fixture stalled the response body and then
changed the target. The new request stayed `Idle` behind the busy
fetcher. The fixture used no live release service and no cluster.

The fix:

* Bound the waits for response headers and for the body. Use
  cancellation, and a whole-transfer deadline, a progress deadline, or
  both. A transfer that makes valid progress over a slow link needs
  enough time.
* Cancel an obsolete download when its target or source changes. The
  reconcile pass does not block while it waits for the writer to stop.
* Start the next writer only after the previous one has stopped.
  Remove partial files on cancellation, and retry through the existing
  re-verification path for completed files.
* Keep transport failures retryable, keep the corruption hold, and keep
  streaming artifacts with bounded memory.

The API, the heartbeat behavior, and digest verification stay the
same. Timeout values need engineering judgment and tests on slow
transfers. A user-configurable timeout or retry policy is a separate
interface decision, and the fix for the unbounded wait needs none.

The fix must agree with the open problem
[staged-slot consistency](open-problems/retargeting-overwrites-staged-releases.md).
A stopped writer can leave a partially updated slot. Stopping the
writer does not by itself make that slot safe to boot.

The tests use local servers that stall before the headers and during
the body. They verify cancellation, retry, and recovery after a changed
source with no process restart. They assert that the reconcile pass
stays responsive and that two writers never overlap. They cover the
removal of partial files, reuse of verified artifacts, slow transfers
that make progress, and unchanged behavior on a digest mismatch.

## Part six: CI runs only pinned executables

This is supply-chain hardening of medium priority. Two kinds of
executable input in CI can change at their origin with no reviewed
change in this repository.

**The certificate workflow runs an unverified tool with the wider
token.** `.github/workflows/releases-cert.yaml` downloads
`lego_v5.2.2_linux_amd64.tar.gz` from the `go-acme/lego` GitHub
release, by version and with no digest. It extracts the binary and runs
it with `LINODE_TOKEN` set from the `RELEASES_CERT_TOKEN` secret. The
workflow and `liken.sh/terraform.tf` describe that token as scoped to
Domains and Object Storage read/write, so it can write the releases
bucket. The release upload key is scoped to that bucket alone. The
workflow uses the token for the DNS-01 challenge, and its next step
uses it again to install the bucket's TLS certificate. Nothing in the
repository limits the token to one zone. The review did not inspect
the live credential.

**External actions use mutable tags.** The workflows and
`.github/actions/build-setup/action.yaml` use tags such as
`actions/checkout@v7`, `actions/setup-go@v6`, and `actions/cache@v6`.
`j178/prek-action@v2` is in `ci.yaml` and in each `component-*.yaml`
workflow. `ci/` writes those workflows from `ci/templates/`, so a pin
changes in the template, and `make workflows` writes it out. A local
`uses: ./...` reference comes from the checked-out repository, so it
needs no upstream SHA.

HTTPS authenticates GitHub and protects the download in transit. It
does not stop someone from replacing a release asset or moving an
action tag at its origin. A later run can then execute different code
with no reviewed pin change in this repository. A replaced `lego`
asset could expose the cloud token. A replaced action gets the
permissions and credentials of its job. The review read the workflow
files and found the missing checks and the mutable references. It
observed no compromise, and it ran no workflow with live credentials.
The risk is a compromise at the origin that no review in this
repository catches. It is not evidence of a TLS bypass.

A checksum fetched beside the executable at run time does not help
when an attacker can replace both files. A reviewed digest committed
here, or a signature checked against an independently trusted key,
gives an integrity check that does not come from that download.

The fix:

* Commit a reviewed SHA-256 for the `lego` archive. Verify it before
  extraction, and execute only the verified contents. A mismatch must
  fail before the step that uses the credential. Narrow the token that
  `lego` gets to DNS.
* Pin each external action by its full commit SHA, and keep the
  human-readable version beside it for maintenance. Keep the local
  composite-action references local.
* Add a reviewable update process for both kinds of pin. Version
  reporting can follow
  [milestone 48](completed/48-check-and-update-dependency-pins.md), but
  action pins are outside the table that milestone watches.

A pin fixes which upstream bytes run. It does not make them
trustworthy. The selected bytes and each later pin update still need
review. Certificate renewal keeps its schedule, and a successful run
behaves as it does now. The fix does not change the OS release API, and
it needs no release-signing design. A new trust root for signed OS
releases is a separate design from the pins on the code these jobs
execute.

The tests verify that a modified archive fails before extraction and
before the token is used, and that matching bytes proceed. A check
confirms that every external action reference has a full commit SHA
and that the local references stay valid. A rehearsal in an authorized
workflow run covers a pin update and the certificate handshake check.
That rehearsal has not run.

## What this milestone does not do

It does not move any pin to a new version. Part six changes how the
pins are written and checked, not which versions they name. It does not change the CLI's credential
handling, the four fetch scripts that take their checksum from the
same origin, or the reproducibility of the squashfs. Those are real
problems, and each is a separate round. It does not resolve the open problems it
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

The unit tests that parts three, five, and six name run in CI, and
need no drill.

Both smoke drills stay green, and every existing test stays green.
