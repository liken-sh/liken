# A parallel walk

Plan 18. The walk reads several title folders at once. At the end of
this plan a full walk of a large library finishes in a fraction of the
time it takes today, and the catalog it writes is the same one.

## The problem

The walk reads one title folder at a time. For each folder it reads the
directory, reads the sidecar, and reads the size and the time of every
file in it, and after
[plan 17](17-every-file-a-title-carries.md) it reads the extras folder
too. Every one of those is a round trip to a network volume, and the
scanner does nothing while it waits.

The work is almost all waiting, and the folders are independent: two
title folders share no state, and their rows are written by key, so the
order they are read in changes nothing. A walk that reads eight folders
at once waits eight times less.

## A fixed pool of workers

A full walk runs a fixed pool of eight workers. A worker takes one
directory, decides what it is, and does the work that directory calls
for. A title folder is scanned into its rows. A grouping folder is read,
and each of its child directories goes back to the pool. So the descent
through a movies volume's grouping folders is parallel as well, and no
one goroutine holds the walk up by classifying folders for the others.

Eight is the number the plan ships, and it is not a knob. The walk is
bound by the volume's round trips, and eight requests in flight keep a
network volume busy without turning a walk into a burst that starves the
players reading the same server. The scanner holds at most eight folders
and one flush buffer, so the memory bound the streaming walk gives is
unchanged. If a drill shows one volume needs a different number, the
number becomes a field on the `Library`, and not before.

One goroutine writes. The workers hand their finished folders to a
single collector, which appends them to the flush buffer, sums the
counts, and flushes a full buffer to the catalog through the one write
client. So the catalog still sees one writer sending bounded batches,
and the `seen` marks still go with the batch that wrote the rows.

The rest of the walk is unchanged. One walk runs at a time, because the
walk mutex already holds a timer walk and a webhook rescan apart. The
epoch is the walk's, so every worker marks with the same one. The prune
runs after the last worker drains. A cancelled context stops the pool
between folders and drains it, so a walk does not run on past a
shutdown.

A directory the walk cannot read marks the pass incomplete, wherever it
is in the tree, and the prune guard then keeps the rows. The walk that
came before this plan marked only a failed read of the root, and skipped
a failed grouping folder without a word, which loses every title under it
and offers them to the prune as departed. The one exception is a
directory below the root that no longer exists. That is a title deleted
while the walk ran, which is an ordinary event on a live volume, and the
next walk reports the deletion.

Two things change that a reader should know. The rows reach the catalog
in a different order, which nothing depends on: every write is an upsert
by key, and every count is a sum. And the sample of unidentified folder
names the log prints is a sample of whichever ten arrived first, where
before it was the first ten in directory order. The count beside it is
exact either way.

## What was considered and set aside

**Several scanner pods for one Library.** Shard the root across a fixed
number of pods and let each walk its share. It needs shard assignment
that survives a pod's replacement, a catalog volume per pod, a prune
that spans the shards, and a report the operator folds from several
scanners. Every one of those is real work, and none of it pays until one
pod saturates the volume it reads. A pool inside the one scanner reaches
that saturation point first, so this is the step to take before the
larger one is worth designing.

**A worker per title folder.** Unbounded goroutines are simple to write
and turn a library of ten thousand titles into ten thousand concurrent
requests against one storage appliance. The point of the pool is the
ceiling.

**Parallel writes to the catalog.** Several workers posting transactions
at once would remove the collector. The write is not the slow part, the
batch ceiling exists to keep a transaction small, and one writer is what
keeps the flush and its `seen` marks in step.

## The proof

The unit tests prove the pool and a single worker read the same tree
into the same rows, the same counts, and the same unidentified total.
They prove a cancelled context ends a walk in flight, and that a read
error in one folder marks the pass incomplete.

The drill runs on `liken-1` against the real volume. Record the duration
of a full walk of the movies library and of the series library before
this change and after it, from the scanner's own `walk complete` line,
and write both numbers here.

## The drill, 2026-08-30

Run on `liken-1`, against the lab's volume over NFS. The before numbers
are release 2026.08.30-011, which read one folder at a time and recorded
the video files alone. The after numbers are 2026.08.30-012, which reads
eight folders at once and records every file a title carries.

| library | titles | before | after |
| --- | --- | --- | --- |
| movies | 1411 | 6.9s, 13.4s, 13.3s | 18.7s |
| series | 156 | 42.0s, 64.3s, 40.5s | 19.2s |

The two columns do not measure the same work. The after walk reads every
file in every title folder, and its season and extras folders with it,
which is 10,675 files for movies where the before walk read 1430, and
24,240 for series where it read 6258. The series library is the clearer
read: about seven times the files in half the time.

The 18.7 seconds is the movies library's first walk after the upgrade,
which wrote 9245 file rows it had never held. Its later walks, which
write only what changed, took 3.6, 7.1, and 13.1 seconds, against 6.9,
13.4, and 13.3 before. So the movies walk reads seven times the files in
about the same time, and the series walk reads about four times the
files in half the time.

The series library gains more because its folders are deeper. Each
series folder costs a read of its season folders, which the pool
overlaps, where a movies title folder is one read at a shallow depth.
