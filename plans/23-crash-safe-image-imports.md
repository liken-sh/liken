# Crash-safe image imports

Milestone 23 — Done

liken machines are meant to be killed without ceremony. The whole
design assumes it: the OS is an initramfs that rebuilds itself from
two files, the documents that matter live in a staged/proven
lifecycle, and a power cut is supposed to cost a machine nothing but
the reboot. Milestone 17's lab work found the one place that promise
didn't hold, and it found it the hard way, twice: a machine killed in
the wrong few seconds after boot could be left permanently unable to
run its own operator.

## The flaw

The flaw is in a layer we don't own. At startup, k3s's embedded
containerd imports every OCI tarball in agent/images, and for each
new digest it extracts the layers into snapshot directories on
clusterState and records the unpack in its metadata database. Those
two writes are not crash-ordered: kill the machine between the
database commit and the extracted files reaching disk, and the
metadata says unpacked while the files are torn. Containerd trusts
its own record, so the same digest is never unpacked again, on any
later boot, no matter how many times the tarball is re-imported.
Every container started from that image dies with `exec format
error`, forever. When the torn image is the machine operator's, the
machine has lost the program that would have reported the problem,
and when it happens on several machines at once (a whole fleet
restarting during a storm, say), the rollout conductor rightly
freezes and the fleet wedges.

## Reproducing it on purpose

The milestone started by producing the failure deliberately, and the
attempts taught more than the plan anticipated.

Three organic attempts — a QEMU hard kill 200ms to 1.3s after the
import of a fresh operator image committed — all came up healthy.
On this containerd (vendored k3s v1.36.2), a small tarball's unpack
is durable by the time the import's own commit lands: the layer is
one 10MB file, and the metadata database's fsync drags the freshly
allocated data blocks into the same ext4 journal transaction. The
window the plan described is real, but it needs either large layers
(hundreds of megabytes of delayed-allocation pages that an unrelated
fsync won't touch) or heavy concurrent I/O, which is exactly the
shape of the milestone-17 incidents: a founding leader mid-reinstall,
etcd and workload pulls all writing at once.

The same kills proved the underlying mechanism live, just against a
different file: a kill one second after a fresh join left
`serving-kubelet.key` existing but zeroed — k3s writes its agent
credentials without fsync — and the agent then looped forever on
`error loading key`, across reboots, a second permanent wedge from
the same crash-unsafe window. That observation widened the fix: the
discard below covers the whole k3s agent directory, not just the
containerd store, because all of it is derived state and any of it
can be torn.

The containerd wedge itself was banked surgically: with the machine
off, zero the operator binary's ELF header inside every committed
snapshot on the state disk (exactly what lost dirty pages look like
after journal replay: file size intact, content zeros), and boot.
The operator pod died with `exec format error`. A reboot re-imported
the same tarball, containerd saw the digest already recorded, skipped
the unpack, and the pod died again — nine restarts across two boots,
healing never. That is the milestone-17 failure, reproduced on
demand.

## The design

The fix is not inside containerd. Its unpack can't be made
transactional from outside, and reaching into its metadata database
to delete individual snapshots would couple init to another program's
private schema, which some k3s upgrade would eventually break. There
is also no configuration line that covers this path: containerd's
`image_pull_with_sync_fs` option applies to CRI-initiated pulls, not
to the startup tarball import. So the imports ride the OS's own
vocabulary for exactly this problem, the staged/proven lifecycle
(machine/staging.go), with three deliberate departures from how
documents ride it.

The record (machine/imports.go) maps each tarball's basename to the
sha256 of its bytes, rendered canonically so a hash comparison
answers "did anything change". It lives in its own store directory
(imports/) beside the other documents'.

Init's half (init/imports.go) runs after storage settles and before
k3s starts, and only when both machineState and clusterState are
durable: without the first there is nowhere to remember a trial, and
without the second the container store resets with every boot and
cannot wedge. The quiet path — the tarballs hash to what the proven
record already names — is almost every boot, and costs one pass over
four files. New digests stage a trial record, durably, before k3s
ever sees the tarballs. And a staged record still standing at boot
is the signal the whole design turns on: the previous boot died
before its imports were proven, so the store may be lying, and init
discards the k3s agent directory wholesale — sparing only the
images/ tarballs this boot just seeded — rather than trust it. OS
images re-unpack from the tarballs, workload images re-pull from
their registries (cheaply, between peers, when milestone 20's
embedded registry is on), and the kubelet's credentials re-mint from
the join token.

The departures from the document lifecycle:

* **No rejection.** A document that fails its trial falls back to an
  older document; a store that fails its trial falls back to a clean
  store. There is nothing to quarantine and no fallback content to
  prefer, so staged either promotes or stands until a boot discards
  and retries.
* **The staged record's existence is the marker.** Documents pair a
  staged file with an attempted marker because the same document
  gets exactly one trial. Imports retry with a wipe each time, so
  the file standing is the whole signal, and even an unreadable one
  marks a dead trial (init never needs to parse it).
* **The discard is deliberately coarse.** The surgical alternative,
  deleting only torn snapshots, requires editing containerd's
  database, and precision that depends on someone else's internals
  is worse than bluntness that depends only on our own state. The
  cost is bounded and rare: a machine pays it only when it died
  inside a window that is minutes long and only open on boots that
  had something new to unpack. Interrupting the discard itself is
  safe for the same reason every step here is: the staged record is
  still standing, so the next boot discards again and converges.

The operator's half (machine-operator/imports.go) is the proof. The
record can't prove itself; only something that watches containers
run from the imported images can vouch for the unpacks, and the
operator is exactly that — its own pod runs from the tarball most
worth proving. The proof is two observations and one barrier. First,
every OS container on this node (every container running a liken.sh/
image) reports Ready: the kubelet's own verdict, which fails for a
torn image the same way it fails for a crash loop, so a half-unpacked
logs relay holds the whole promotion. Second, syncfs on the container
store's filesystem: the pods only prove the images that run here, and
a tarball whose image never schedules on this node (the cluster
operator, on most machines) could still be latently torn — until its
dirty pages are on disk, at which point no tear is possible at all.
One syscall turns "what we can see serves" into "every byte the
imports wrote is durable". Only then does the record promote. The
plan's open question about defining the expected pod set per node
dissolved: the set is whatever OS containers are present, the
operator itself is always among them, and syncfs covers everything
that isn't.

The machine reports all of it: boot.importsSource (Staged or
Proven), boot.importsHash, boot.importsDiscarded on the boot that
threw a store away, and an ImportsConverged condition whose Proving
reason maps to the Updating phase (a trial ordinarily proves within
seconds of the operator starting).

## What the lab showed

The heal drill ran the full chain deliberately. A machine wedged the
old way (torn snapshots, old build) booted the fixed image: init
staged a trial of the new tarballs. A hard kill landed right after
the imports, before the operator could promote, and the snapshots
were then zeroed on disk again for good measure — a dead trial atop
real damage. The next boot printed `the previous boot's imports were
never proven; discarding the container store`, staged a fresh trial,
unpacked everything from the tarballs, and the operator came up,
proved the imports, and promoted: ImportsConverged True,
importsDiscarded true, no human touch. The negative drill then
hard-killed the same machine once settled: the next boot took the
quiet path (`4 image tarballs proven`) and discarded nothing.

The promotion barrier's failure mode got exercised by accident,
usefully: mid-drill, the operator pod predated the manifest that
mounts the container store, syncfs had no filesystem to open, and
the condition reported PromotionFailed with the exact error while
init's half kept the machine healthy. The record stayed staged —
unprovable rather than wrongly proven — which is the behavior the
barrier exists for.

The whole fleet then rolled onto the build, leaders one at a time,
every machine's first fixed boot staging and proving its trial.

## Out of scope, recorded

A machine that tears, comes up broken, and then stays up has no
operator to ask for a reboot; the heal lands on whatever boot comes
next. The failure that motivated this milestone arrived with reboots
attached (storms, reinstalls), so the next boot was never far. If a
standing-but-unprovable trial turns out to linger in practice, a
watchdog on the trial's age — the shape provingWatch already gave
reboots — is the natural extension.

Credential files torn outside a trial window (a kill during cert
renewal on a settled machine, with no new tarballs and so no marker
standing) are not covered. That is k3s's file to fsync; the discard
heals it only when a trial happens to be standing.
