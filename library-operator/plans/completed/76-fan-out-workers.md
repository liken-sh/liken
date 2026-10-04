# 76, Fan-out workers

This plan, written on 2026-10-02, lets one worker `Job` of a heavy
fact run on several nodes at once. Each pod of the `Job` takes a share
of the fact's work list. The `appearances` and `trickplay` workers
both take it. Built on 2026-10-02 and proved on the lab. The open
questions below are the open problem
[`a-fan-out-share-is-one-title-folder`](../open-problems/a-fan-out-share-is-one-title-folder.md).

## The problem

A `Library` runs one worker `Job` for each heavy fact, and that `Job`
runs one pod (plan 75, `factworkerjob.go`). The `appearances` worker
decodes every frame of each feature, about 5 minutes for a film on one
GPU. A home library of about 1,450 films is then about 110 hours of
work on one node, and the 24-hour deadline of a worker `Job` ends it
four times before the list is done. The other nodes' GPUs stay idle
for all of that time. The `trickplay` worker has the same shape: one
pod reads the whole list, one video at a time.

## The design

**The field.** Each worker fact's block in the `Library` spec takes
`parallelism`: `spec.appearances.parallelism` and
`spec.trickplay.parallelism`. It is the number of pods the fact's
worker `Job` runs, from 1 to 16, and the CRD defaults it to 1. At 1
the operator builds the same `Job` as before this plan, field for
field, so a `Library` that does not set the field sees no change. The
cap keeps one typing error from asking for hundreds of pods, each
with its own claim and its own memory limit.

**The `Job`.** At N above 1 the worker `Job` is an Indexed `Job`:
`completionMode: Indexed`, `completions: N`, and `parallelism: N`.
The `Job` controller gives each pod its index in
`JOB_COMPLETION_INDEX`, and the operator gives N to every pod in
`LIBRARY_WORKER_PARALLELISM`. The `Job` completes when every index
completes.

**The share.** Every pod reads the same work list
(`.liken/worklists/<namespace>/<library>/<fact>.jsonl`). Pod i takes
each video whose title folder hashes to i, modulo N. The hash is
FNV-1a over the folder's path relative to the root, so every pod
computes the same split, on any node and in any process. The title
folder is the folder a rescan names: the series folder for a series,
and the movie's title folder with its extras for a movie. So a
season's episodes and their shared `.liken` ledger go to one pod, and
each pod asks for rescans of folders that no other pod touches. A
video at the root, with no title folder, hashes by its own path, and
the `.liken` ledger at the root can then take writes from two pods.
The update door (`volumeupdate.go`) covers that case, as it covers two
clusters on one volume. The pods of one `Job` share its name, which
the volume writer puts in each temporary file's name, so each pod adds
its index there, and two pods never stage one temporary. Each pod logs its index and its share: "index
1 of 4 takes 361 of the 1447 videos".

**Retries.** At N above 1 the `Job` sets `backoffLimitPerIndex` to the
`backoffLimit` the worker has today, 2, and states no `backoffLimit`.
Kubernetes then retries a failed index up to twice and leaves the
indexes that finished alone. A retried index reads its share again,
and passes over each video that has an attempt in the ledger from
after the list was written, so a retry does not decode again what the
failed pod finished. A `backoffLimit` beside it would count the
failures of every index together and end the whole `Job` early, so the
`Job` states none. `maxFailedIndexes` stays unset: an index that
exhausts its retries does not stop the others, and the `Job` ends
`Failed` when all of them have ended. `backoffLimitPerIndex` is GA in
Kubernetes 1.33, and `liken` ships 1.36.

**Placement.** At N above 1 the pod carries one
`topologySpreadConstraints` entry: `maxSkew: 1` over
`kubernetes.io/hostname`, over the pods of the `Library`'s worker of
the fact, with `whenUnsatisfiable: ScheduleAnyway`. The scheduler
prefers a node that holds fewer of these pods, and places a pod where
it must when no such node can take it.

`DoNotSchedule` was set aside, because the scheduler counts every node
as a place to spread to, including a node with no matching GPU. The
DRA filter runs separately. On a cluster with five nodes and two GPUs,
a worker with N of 3 places one pod on each GPU node. The third pod
cannot go to a GPU node, because the skew to the empty nodes would be
2, and it cannot go to an empty node, because the claim finds no
device there. It stays `Pending` until another pod ends. With
`ScheduleAnyway` the claim alone decides which nodes can take the pod,
and the spread only ranks them. The third pod runs beside another on
a GPU node. `liken` publishes the render node with
`allowMultipleAllocations`, so two claims can share one GPU. A worker
with no render block decodes in software, and its pods go to every
node that has room.

Each pod gets its own `ResourceClaim` from the worker's
`ResourceClaimTemplate` (`renderclaim.go`), so N needs no change to
the template.

**Due and done.** The operator decides whether a worker is due from
the `Job`'s conditions (`factWorkerDue`): a worker of the fact that
has no `Complete` or `Failed` condition holds the next list back. An
Indexed `Job` takes one of those conditions only when every index has
ended, so the rule counts the `Job`, not its pods, and needs no
change. The work list annotation and the list the operator records as
taken are both on the `Job`, so N pods start one worker per list.

**Reports.** A worker writes no catalog row. Its one report is a
rescan request for each title folder it finished, and the shares hold
disjoint title folders, so no two pods ask for one folder. The
operator holds up to 64 rescan paths for a `Library` while its `Job`
runs, and N pods send them up to N times as fast. Past 64 the
operator runs one full walk in their place, which bounds the cost.

**The deadline.** `activeDeadlineSeconds` stays at 24 hours for the
whole `Job`. When it passes, Kubernetes stops every pod, the videos
each pod had not reached stay in the gap, and the next library `Job`'s
list starts a new worker, as it does at N of 1. With N pods, a list
reaches the deadline after N times as much work.

## What was set aside

- **A shared queue with leases.** Each pod would take the next video
  from a queue and hold a lease on it. That balances the work better
  than a hash, but it needs a store that every pod writes and a lease
  that a stopped pod gives up. The volume offers no lock, and a worker
  holds no catalog. The hash split needs neither.
- **One `Job` for each node.** The operator would read the nodes and
  their GPUs and start one `Job` on each. The operator would then own
  placement, a node that leaves would hold a `Job` that cannot run,
  and the due rule would count several `Job`s for one list. An
  Indexed `Job` gives the same pods with one object.
- **N from the count of GPUs.** The operator could count the devices
  of the render block's class and set N itself. That needs a read of
  the `ResourceSlice`s and a rule for devices that other workloads
  hold. The field can take an `auto` value later, and nothing in this
  design prevents that.
- **An Indexed `Job` at N of 1.** One Indexed pod behaves as today, but
  the `Job` would change for every `Library` and every test of it. At
  1 the operator builds the `Job` it built before, and a pod with no
  `JOB_COMPLETION_INDEX` takes the whole list.

## Open questions

- **Uneven shares.** A hash by title folder balances the count of
  folders, not the hours. A long series lands whole in one pod, so
  that pod can run hours after the others end. A series library may
  want a split by season folder, with the ledger write through the
  update door. Not measured yet.
- **A pod name that is too long.** The pod of an Indexed `Job` takes
  the hostname `<job>-<index>`. A `Library` with a name near the limit
  of a `Job` name can pass the 63 characters of a hostname. The name
  rule of a worker `Job` is the one every `Job` of the operator uses,
  and no test has tried a long name with N above 1.
- **A list that changes while the pods start.** Each pod resolves the
  title folder of each video when it starts. A folder that is created
  or removed between two pods' starts can put one video in two shares
  or in none. A video in none stays in the gap for the next list, and
  a video in two is decoded twice, and the update door keeps the
  ledger whole.

## The proof

On a cluster with at least three nodes that offer the render node, a
`Library` with `spec.appearances.parallelism: 3` runs one worker `Job`
with 3 pods, on 3 different nodes. Each pod logs its index and its
share. The three shares add up to the length of the list, and no
title folder appears in two pods' logs. When the `Job` completes, the
gap in `status.gaps` holds only the videos the ledger marks as failed.
A pod deleted in the middle of its share is replaced, the new pod
passes over the videos the deleted pod finished, and the other two
indexes do not run again.
