# Crash-safe image imports

Milestone 23 — Completed. A machine that is killed during an image
unpack heals itself, because a later boot discards a container store
that was never proven.

liken machines can be killed without warning. The design assumes this:
the OS is an initramfs that rebuilds itself from two files, the
documents that matter live in a staged/proven lifecycle, and a power
cut costs a machine nothing but the reboot. Milestone 17's lab work
found the one place where that was not true, twice. A machine killed in
the wrong few seconds after a boot could be left permanently unable to
run its own operator.

## The flaw

The flaw is in a layer that liken does not own. At startup, k3s's
embedded containerd imports every OCI tarball in agent/images. For each
new digest, containerd extracts the layers into snapshot directories on
clusterState and records the unpack in its metadata database. These two
writes are not ordered against a crash. If the machine is killed
between the database commit and the moment the extracted files reach
the disk, the metadata says the image is unpacked while the files are
torn. Containerd trusts its own record, so it does not unpack the same
digest again on any later boot, no matter how many times the tarball is
imported again. Every container started from that image dies with `exec
format error`, permanently. If the torn image is the machine operator's
own image, the machine has lost the program that would report the
problem. If this happens on several machines at once, for example a
whole fleet restarting during a power outage, the rollout conductor
correctly freezes and the fleet stalls.

## Reproducing the flaw on purpose

Three natural attempts did not reproduce the flaw. Each used a QEMU
hard kill, timed 200ms to 1.3s after the import of a fresh operator
image committed, and each came up healthy. On this containerd (vendored
k3s v1.36.2), a small tarball's unpack is durable by the time the
import's own commit lands. The layer is one 10MB file, and the metadata
database's fsync call drags the freshly allocated data blocks into the
same ext4 journal transaction. The failure window described in the plan
is real, but it needs either large layers, with hundreds of megabytes
of delayed-allocation pages that an unrelated fsync does not touch, or
heavy concurrent input and output. This is the shape of the
milestone-17 incidents: a founding leader mid-reinstall, with etcd and
workload pulls all writing at once.

The same kills did prove the underlying mechanism live, against a
different file. A kill one second after a fresh join left
`serving-kubelet.key` present but zeroed, because k3s writes its agent
credentials without an fsync call. The agent then looped forever on
`error loading key`, across reboots. This is a second permanent failure
from the same crash-unsafe window. This observation widened the fix.
The discard described below covers the whole k3s agent directory, not
only the containerd store, because all of that directory is derived
state and any part of it can be torn.

A precise method reproduced the containerd failure itself. With the
machine off, the drill zeroed the operator binary's ELF header inside
every committed snapshot on the state disk. This is what lost dirty
pages look like after a journal replay: the file size stays intact, but
its content is zeros. The machine then booted. The operator pod died
with `exec format error`. A reboot imported the same tarball again,
containerd saw the digest already recorded, skipped the unpack, and the
pod died again. This ran for nine restarts across two boots, with no
heal. This is the milestone-17 failure, reproduced on demand.

## The design

The fix does not live inside containerd. Containerd's unpack cannot be
made transactional from outside, and edits to its metadata database to
delete individual snapshots would couple init to another program's
private schema, which a future k3s upgrade can break. No configuration
line covers this path either: containerd's `image_pull_with_sync_fs`
option applies to CRI-initiated pulls, not to the startup tarball
import. The imports therefore use the OS's own vocabulary for exactly
this kind of problem, the staged/proven lifecycle (machine/staging.go),
with three deliberate differences from how documents use it.

The record (machine/imports.go) maps each tarball's basename to the
sha256 hash of its bytes, rendered canonically so that a hash
comparison answers the question "did anything change". It lives in its
own store directory, imports/, beside the directories for the other
documents.

init's half of the work (init/imports.go) runs after storage settles
and before k3s starts, and only when both machineState and clusterState
are durable. Without machineState, there is nowhere to remember a
trial. Without clusterState, the container store resets with every boot
and cannot get stuck. The quiet path, where the tarballs hash to what
the proven record already names, covers almost every boot and costs one
pass over four files. New digests stage a trial record, durably, before
k3s sees the tarballs. A staged record still standing at boot is the
signal that the whole design turns on. It means the previous boot died
before its imports were proven, so the store can be wrong. In that
case, init discards the k3s agent directory wholesale and keeps only
the images/ tarballs that this boot just seeded, rather than trust the
directory's contents. OS images unpack again from the tarballs.
Workload images pull again from their registries, cheaply between peers
when milestone 20's embedded registry is on. The kubelet's credentials
mint again from the join token.

The design differs from the document lifecycle in three ways:

* **No rejection.** A document that fails its trial falls back to an
  older document. A store that fails its trial falls back to a clean
  store instead. There is nothing to quarantine and no fallback content
  to prefer. A staged record therefore either promotes, or stands until
  a boot discards the store and retries.
* **The staged record's existence is the marker.** Documents pair a
  staged file with an attempted marker, because the same document gets
  exactly one trial. Imports retry with a wipe each time, so the file
  standing alone is the whole signal. Even an unreadable file marks a
  dead trial, because init does not need to parse it.
* **The discard is deliberately coarse.** The more precise
  alternative, a delete of only the torn snapshots, requires edits to
  containerd's database. Precision that depends on another program's
  internals is worse than a simpler method that depends only on our own
  state. The cost is bounded and rare. A machine pays it only when it
  died inside a window that lasts minutes, and that window opens only
  on boots that had something new to unpack. An interruption of the
  discard itself is safe, for the same reason every step here is safe:
  the staged record is still standing, so the next boot discards again
  and converges.

The operator's half of the work (machine-operator/imports.go) supplies
the proof. The record cannot prove itself. Only something that watches
containers run from the imported images can vouch for the unpacks, and
the operator is exactly that, because its own pod runs from the tarball
most worth proving. The proof rests on two observations and one
barrier. First, every OS container on this node, which is every
container running a liken.sh/ image, must report Ready. This is the
kubelet's own verdict, and it fails for a torn image the same way it
fails for a crash loop, so a half-unpacked logs relay holds up the
whole promotion. Second, the operator calls syncfs on the container
store's filesystem. The running pods prove only the images that run on
this node, and a tarball whose image never schedules on this node, for
example the cluster operator on most machines, can still be latently
torn until its dirty pages reach the disk. After that, no tear is
possible at all. This one syscall turns "what we can see is serving"
into "every byte the imports wrote is durable". Only then does the
record promote. The plan's open question about the expected pod set for
each node no longer applies: the set is whatever OS containers are
present, the operator itself is always among them, and syncfs covers
everything else.

The machine reports all of this: boot.importsSource (Staged or Proven),
boot.importsHash, boot.importsDiscarded on the boot that discarded a
store, and an ImportsConverged condition whose Proving reason maps to
the Updating phase. A trial usually proves within seconds of the
operator starting.

## What the lab showed

The heal drill ran the full chain deliberately. A machine that was
stuck the old way, with torn snapshots and an old build, booted the
fixed image, and init staged a trial of the new tarballs. A hard kill
landed right after the imports, before the operator could promote, and
the snapshots were then zeroed on disk again. This made a dead trial on
top of real damage. The next boot printed `the previous boot's imports
were never proven; discarding the container store`, staged a fresh
trial, and unpacked everything from the tarballs. The operator came up,
proved the imports, and promoted: ImportsConverged True and
importsDiscarded true, with no human step. The negative drill then
hard-killed the same machine after it settled. The next boot took the
quiet path (`4 image tarballs proven`) and discarded nothing.

The promotion barrier's failure mode was also exercised, by accident.
Mid-drill, the operator pod started before the manifest that mounts the
container store, so syncfs had no filesystem to open. The condition
reported PromotionFailed with the exact error, while init's half kept
the machine healthy. The record stayed staged: unprovable rather than
wrongly proven, which is the behavior the barrier exists to produce.

The whole fleet then rolled onto the build, leaders one at a time, and
every machine's first fixed boot staged and proved its own trial.

## Out of scope, recorded

A machine that tears, comes up broken, and then stays up has no
operator to ask for a reboot. The heal lands on the next boot, whenever
that comes. The failure that motivated this milestone arrived together
with reboots, from power outages and reinstalls, so the next boot was
never far away. If a standing but unprovable trial lingers in practice,
a watchdog on the trial's age, in the same shape that provingWatch
already gives reboots, is the natural extension.

Credential files torn outside a trial window are not covered, for
example a kill during a certificate renewal on a settled machine, with
no new tarballs and therefore no marker standing. That file belongs to
k3s to fsync. The discard here heals it only when a trial is standing
at the time.
