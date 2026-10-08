# 78, Workers are catalog peers

This plan, written on 2026-10-07, gives each pod of a worker `Job` its
own catalog agent, so the worker reads its fact's gap from the catalog
instead of from a work list on the media volume, and splits that gap
among its pods by hours instead of by a hash. It also removes the
`.liken` directory at the library root. It answers the open problem "A
fan-out share is one title folder", which the commit that built it
deleted. Built on 2026-10-07. Its drill on `liken-1` on 2026-10-07 found
that a fresh copy reads a partial gap, and
[plan 79](79-one-video-per-index.md) replaces its worker design.

## The problem

**The work list is job state on the media volume.** The close
container of every library `Job` writes each heavy fact's gap to
`<root>/.liken/worklists/<namespace>/<library>/<fact>.jsonl`, and the
fact's worker `Job` reads it there (`worklist.go`). The trickplay
sheets and the appearances ledgers on the volume are facts about the
media, and the design keeps every fact beside the media. A work list
is not a fact. It is a copy of one catalog query for one run, and it
is on the volume only because the volume is the one place that the
library `Job` and the worker `Job` both mount. The library `Job` holds
the catalog and no Kubernetes credential, and the worker holds a
credential for nothing and no catalog.

**Shares are even by count, not by hours.** A worker with
`parallelism` above 1 is an Indexed `Job`, and each pod takes the title
folders whose name hashes to its index (plan 76). The hash balances the
count of folders. A series folder holds every episode of the series,
so one long series makes one pod run for hours after the others end.
That happened repeatedly in the first large runs of the `trickplay`
and `appearances` workers.

**A share can drift from the list.** Each pod resolves the title folder
of each video when it starts, so a folder created or removed between
two pods' starts can put one video in two shares or in none. Both
outcomes cost time and lose nothing.

Plan 76 set aside a shared queue because "the volume offers no lock,
and a worker holds no catalog". This plan gives the worker a catalog.

## The design

### The worker's agent

Each pod of a worker `Job` runs the Corrosion agent as a native
sidecar, the way the library `Job`'s pod does, and reads the catalog
through it on loopback. No new API is involved: the agent's own API is
the one every catalog reader already uses.

**The copy.** Where the agent keeps its copy is the cluster owner's
choice, in the `Catalog`, beside the classes it already names for the
library `Job`s' and the screens' copies:

- **`spec.workers.storageClassName` unset.** Each pod's agent keeps its
  copy in an `emptyDir`, which is on the node's pod-ephemeral storage.
  The copy syncs in full when the pod starts and is deleted when the
  pod ends. This is the default, and both `liken-1` and the home
  cluster start here. Unlike the `Catalog`'s other classes, unset does
  not mean the cluster's default class. On most clusters that is
  `local-path`, which would tie each copy to one node and keep it
  there, and a worker copy is a cache that needs neither.
- **`spec.workers.storageClassName` set.** The operator stands one
  claim for the namespace's workers, `<catalog>-workers`, on that
  class, at the `Catalog`'s `storage.size`. Each pod mounts the
  subdirectory `<library>-<fact>-<index>`, through `subPathExpr` and the
  `JOB_COMPLETION_INDEX` that the `Job` controller sets. One `Library`
  runs one worker of a fact at a time, so no two agents open one
  directory. With a per-node class, a pod that lands on the node where
  its index ran before finds its copy there and syncs only what changed.

A change of the field moves no data, because every copy rebuilds from
its peers. Pods that start after the change use the new place, and a
running pod finishes where it started.

The two choices trade sync time against disk. On a home cluster, a
copy of the catalog is about 720 MB, and a node's pod-storage
partition is 8.3 GB with 4.4 to 7.1 GB free, before any worker copy.
Up to one copy per library, fact, and index would land there, so a
per-node class fits only on a node with room for them. Plan 68
measured a fresh agent's sync of a 600,000-row catalog at about 400 s
on a workstation, and the drill measures the real catalog.

**The baseline.** A worker starts after a library `Job`'s enrich run
finishes, and the worker `Job`'s annotation names that `Job`. Before a
pod reads the gap, it waits until its copy holds that run's version,
with the wait the enrich phases already make before they read a gap
(`awaitCatalogSync`). Every pod then reads the gap from a copy that
holds at least the same writes.

**No hand-off.** The worker writes no catalog row, so its agent holds
no write that a catalog pod must confirm, and the pod exits without
one. Its report stays a rescan request for each title folder it
finishes, and the walk of that folder reads the new facts from the
volume.

### The gap and the share

**The gap.** Each pod reads its fact's gap from its copy, with the
query and the refresh time that the close container uses to write the
list today. The close container stops writing lists, and it deletes
its own Library's `worklists` directory on its next run, so the files
leave the volume without a person.

**The share, by hours.** Every pod groups the gap by title folder and
gives each folder its runtime, the sum of its videos' lengths, which
the gap query already returns. It sorts the folders longest first, with
the path as the tie-break, and deals each folder to the pod whose total
is lowest so far. Every pod reads the same gap and runs the same rule,
so every pod computes the same split, and no pod writes anything to
coordinate. At a parallelism of 1, the one pod takes the whole gap.

The rule keeps a title folder in one pod, because the folder's ledger
is one file. A series of 400 episodes is still one pod's work. The
drill measures how much unevenness that leaves, and the measurement
decides whether a later plan splits a series by season or adds leases
in a catalog table. Leases are possible now because the worker holds a
catalog. They are not built here, because the split by hours may be
enough.

**The drift.** A pod whose copy holds a later walk than another pod's
can read a different gap. A video in the later gap and not the earlier
is in at most one share or in none, and a video in none stays in the
gap for the next worker. Both outcomes are the ones plan 76 accepted.

### The library root

The root of a `Library` holds title folders and grouping folders, and
the walk reads titles only below it. A `.liken` directory at the root
holds no fact: on `liken-1` on 2026-10-07, it held the work lists of
both clusters that mount the volumes, and ledgers that older releases
wrote when they read the root, or a `trailers` folder at the root, as a
title. The close container of every library `Job` removes the
directory. A rescan of the root reads no title in the root itself, and
the guides state that the root never holds a video file of its own.

### What a person sees

- The `.liken` directory at each library's root, with the work lists
  in it, leaves the volume after the `Library`'s next library `Job`.
- A worker `Job`'s pods take a little longer to start on a node where
  they have not run, while the agent catches up.
- `kubectl get pvc` lists `<catalog>-workers` when
  `spec.workers.storageClassName` is set.
- The worker's log names its share in folders and hours.

## What was set aside

- **An API in the catalog pod that hands out work.** It balances the
  work best, but it is a new API, and each API invites the next.
- **The work list on its own claim.** It moves the file off the media
  volume and fixes nothing else, and pods on several nodes need a
  storage class that every node mounts.
- **The work list in a `ConfigMap` or in the `Job`.** A list can be
  thousands of videos, which does not belong in the control plane.
- **Copies on a per-node claim by default.** A copy that stays on its
  node syncs only what changed, but a home cluster's pod-storage
  partitions are too small to hold one copy per library, fact, and
  index beside the catalog pods' and the screens' copies. The class is
  the cluster owner's choice instead.

## The proof

The tests run a worker pod's gap read and split against the in-memory
catalog: the same gap gives every index the same split, the split by
hours is no worse than the hash split on a gap with one long series,
and a pod waits for its baseline before it reads. The operator's tests
build the worker `Job` with an `emptyDir` and with a claim.

The drill on `liken-1`: turn on `trickplay` for `series` at a
parallelism of 4, and record each pod's wall time, the agent's sync
time and peak memory at pod start, and the free space on the node's
pod-ephemeral storage while the copies stand.

## What the drill found

On `liken-1` on 2026-10-07, `appearances` turned on for `series` at a
parallelism of 4 started a worker `Job` one second after the library
`Job`'s enrich run, for a gap of 6,822 videos. The scheduler put two
pods on `liken-1` and two on `stick-1`, a 4 GB machine that drives one
screen, because nothing kept them off it. All four were running two
minutes after the `Job` started. Each agent applied the catalog in
batches of 100 to 500 rows, at about 25 millicores and 50 to 100 MiB in
its first minutes.

The first pod to read its gap, five minutes after the `Job` started,
read 415 videos, not 6,822. Its copy held the writes of the library
`Job` that started the worker, which is all the sync wait checks, and
little else, because an `emptyDir` copy starts empty. The drill was
stopped there. The root `.liken` removal worked: the `series` walk at
20:20 logged that it removed the directory.
