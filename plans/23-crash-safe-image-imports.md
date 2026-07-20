# Crash-safe image imports

Milestone 23 — Done

liken machines are meant to be killed without warning. The whole
design assumes this: the OS is an initramfs that rebuilds itself from
two files, the documents that matter live in a staged/proven
lifecycle, and a power cut is supposed to cost a machine nothing but
the reboot. Milestone 17's lab work found the one place where that
promise did not hold, and it found this the hard way, twice: a machine
killed in the wrong few seconds after boot could be left permanently
unable to run its own operator.

## The flaw

The flaw sits in a layer that liken does not own. At startup, k3s's
embedded containerd imports every OCI tarball in agent/images. For
each new digest, containerd extracts the layers into snapshot
directories on clusterState and records the unpack in its metadata
database. These two writes are not ordered against a crash: if the
machine is killed between the database commit and the moment the
extracted files reach disk, the metadata says the image is unpacked
while the files are torn. Containerd trusts its own record, so it
never unpacks the same digest again, on any later boot, no matter how
many times the tarball is re-imported. Every container started from
that image dies with `exec format error`, permanently. When the torn
image is the machine operator's own image, the machine has lost the
program that would have reported the problem. When this happens on
several machines at once, for example a whole fleet restarting during
a power outage, the rollout conductor correctly freezes and the fleet
stalls.

## Reproducing the flaw on purpose

The milestone started by producing the failure deliberately, and the
attempts to reproduce it taught more than the plan expected.

Three natural attempts failed to reproduce the flaw. Each used a QEMU
hard kill, timed 200ms to 1.3s after the import of a fresh operator
image committed, and each came up healthy. On this containerd
(vendored k3s v1.36.2), a small tarball's unpack is durable by the
time the import's own commit lands: the layer is one 10MB file, and
the metadata database's fsync call drags the freshly allocated data
blocks into the same ext4 journal transaction. The failure window
described in the plan is real, but it needs either large layers
(hundreds of megabytes of delayed-allocation pages that an unrelated
fsync will not touch) or heavy concurrent input and output. This is
exactly the shape of the milestone-17 incidents: a founding leader
mid-reinstall, with etcd and workload pulls all writing at once.

The same kills did prove the underlying mechanism live, against a
different file. A kill one second after a fresh join left
`serving-kubelet.key` existing but zeroed, because k3s writes its
agent credentials without an fsync call. The agent then looped forever
on `error loading key`, across reboots: a second permanent failure
from the same crash-unsafe window. This observation widened the fix:
the
discard described below covers the whole k3s agent directory, not
just the containerd store, because all of that directory is derived
state, and any part of it can be torn.

The team reproduced the containerd failure itself with a precise
method. With the machine off, they zeroed the operator binary's ELF
header inside every committed snapshot on the state disk. This is
exactly what lost dirty pages look like after journal replay: the
file size stays intact, but its content is zeros. They then booted
the machine. The operator pod died with `exec format error`. A reboot
re-imported the same tarball, containerd saw the digest already
recorded, skipped the unpack, and the pod died again. This ran for
nine restarts across two boots, with no healing. This is the
milestone-17 failure, reproduced on demand.

## The design

The fix does not live inside containerd. Containerd's unpack cannot be
made transactional from outside, and reaching into its metadata
database to delete individual snapshots would couple init to another
program's private schema, which some future k3s upgrade would
eventually break. There is also no configuration line that covers this
path: containerd's `image_pull_with_sync_fs` option applies to
CRI-initiated pulls, not to the startup tarball import. So the imports
use the OS's own vocabulary for exactly this kind of problem, the
staged/proven lifecycle (machine/staging.go), with three deliberate
departures from how documents normally use it.

The record (machine/imports.go) maps each tarball's basename to the
sha256 hash of its bytes, rendered canonically so that a hash
comparison can answer the question "did anything change". It lives in
its own store directory (imports/), beside the directories for the
other documents.

init's half of the work (init/imports.go) runs after storage settles
and before k3s starts, and only when both machineState and
clusterState are durable. Without machineState, there is nowhere to
remember a trial. Without clusterState, the container store resets
with every boot and cannot get stuck. The quiet path, where the tarballs
hash to what the proven record already names, covers almost every
boot, and costs one pass over four files. New digests stage a trial
record, durably, before k3s ever sees the tarballs. A staged record
still standing at boot is the signal that the whole design turns on:
it means the previous boot died before its imports were proven, so
the store may be lying. In that case, init discards the k3s agent
directory wholesale, sparing only the images/ tarballs that this boot
just seeded, rather than trust the directory's contents. OS images
re-unpack from the tarballs. Workload images re-pull from their
registries (cheaply, between peers, when milestone 20's embedded
registry is on). The kubelet's credentials re-mint from the join
token.

The design departs from the document lifecycle in three ways:

* **No rejection.** A document that fails its trial falls back to an
  older document. A store that fails its trial falls back to a clean
  store instead: there is nothing to quarantine and no fallback
  content to prefer. So a staged record either promotes, or stands
  until a boot discards the store and retries.
* **The staged record's existence is the marker.** Documents pair a
  staged file with an attempted marker, because the same document
  gets exactly one trial. Imports retry with a wipe each time, so the
  file standing alone is the whole signal. Even an unreadable file
  marks a dead trial, because init never needs to parse it.
* **The discard is deliberately coarse.** The more precise
  alternative, deleting only the torn snapshots, requires editing
  containerd's database. Precision that depends on another program's
  internals is worse than a simpler approach that depends only on our
  own state. The cost is
  bounded and rare: a machine pays it only when it died inside a
  window that lasts minutes and only opens on boots that had something
  new to unpack. Interrupting the discard itself is safe, for the same
  reason every step here is safe: the staged record is still standing,
  so the next boot discards again and converges.

The operator's half of the work (machine-operator/imports.go) supplies
the proof. The record cannot prove itself; only something that watches
containers run from the imported images can vouch for the unpacks, and
the operator is exactly that, because its own pod runs from the
tarball most worth proving. The proof rests on two observations and
one barrier. First, every OS container on this node (every container
running a liken.sh/ image) must report Ready. This is the kubelet's
own verdict, and it fails for a torn image the same way it fails for a
crash loop, so a half-unpacked logs relay holds up the whole
promotion. Second, the operator calls syncfs on the container store's
filesystem. The running pods only prove the images that run on this
node, and a tarball whose image never schedules on this node (the
cluster operator, on most machines) could still be latently torn,
until its dirty pages reach disk, at which point no tear is possible
at all. This one syscall turns "what we can see is serving" into
"every byte the imports wrote is durable". Only then does the record
promote. The plan's open question about defining the expected pod set
for each node dissolved on its own: the set is whatever OS containers
are present, the operator itself is always among them, and syncfs
covers everything else.

The machine reports all of this: boot.importsSource (Staged or
Proven), boot.importsHash, boot.importsDiscarded on the boot that
discarded a store, and an ImportsConverged condition whose Proving
reason maps to the Updating phase. A trial ordinarily proves within
seconds of the operator starting.

## What the lab showed

The heal drill ran the full chain deliberately. A machine that had
gotten stuck the old way (torn snapshots, old build) booted the fixed
image: init
staged a trial of the new tarballs. A hard kill landed right after the
imports, before the operator could promote, and the snapshots were
then zeroed on disk again for good measure, creating a dead trial on
top of real damage. The next boot printed `the previous boot's imports
were never proven; discarding the container store`, staged a fresh
trial, unpacked everything from the tarballs, and the operator came
up, proved the imports, and promoted: ImportsConverged True,
importsDiscarded true, with no human involved. The negative drill then
hard-killed the same machine once it had settled: the next boot took
the quiet path (`4 image tarballs proven`) and discarded nothing.

The promotion barrier's failure mode was exercised by accident, and
usefully. Mid-drill, the operator pod started before the manifest that
mounts the container store, so syncfs had no filesystem to open. The
condition reported PromotionFailed with the exact error, while init's
half kept the machine healthy. The record stayed staged, unprovable
rather than wrongly proven, which is exactly the behavior the barrier
exists to produce.

The whole fleet then rolled onto the build, leaders one at a time,
with every machine's first fixed boot staging and proving its own
trial.

## Out of scope, recorded

A machine that tears, comes up broken, and then stays up has no
operator to ask for a reboot. The heal lands on whatever boot comes
next. The failure that motivated this milestone arrived together with
reboots (power outages, reinstalls), so the next boot was never far
away. If a standing but unprovable trial turns out to linger in
practice, a watchdog on the trial's age, the same shape that
provingWatch already gives reboots, is the natural extension.

Credential files torn outside a trial window are not covered: for
example, a kill during certificate renewal on a settled machine, with
no new tarballs and so no marker standing. That file belongs to k3s to
fsync. The discard here heals it only when a trial happens to be
standing at the time.
