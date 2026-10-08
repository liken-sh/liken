# 79, One video per index

This plan, written on 2026-10-08, makes each worker `Job` an Indexed
`Job` with one video per index. The library `Job` publishes the fact's
missing videos on the bus, one retained message per index, and each
pod reads its own message and works that one video. The `Job`
controller is the queue: it starts the next index whenever a pod ends,
up to `parallelism` at once, and each pod goes through the scheduler on
its own. It replaces the worker design of
[plan 78](78-workers-are-catalog-peers.md), whose catalog
copies and split by hours it removes. Built, and drilled on `liken-1`
on 2026-10-08.

## The problem

Plan 78 gave each worker pod a catalog agent, so the worker read its
gap from its own copy. Its drill on `liken-1` on 2026-10-07 found that
a copy in an `emptyDir` starts nearly empty, and the sync wait checks
only the writes of the library `Job` that started the worker. The
first pod passed the wait with part of the catalog and read 415 of the
6,822 videos in the `series` gap. A copy can prove that it holds every
writer's newest version only from a list of those writers that some
complete copy publishes. Each copy also costs a sync of the whole
catalog at every pod start, and memory for the agent beside the work.

The split by hours was also still static. Each pod computed its share
once, and a series of many episodes stayed one pod's work, because the
split kept a title folder in one pod.

The library `Job` already holds a complete copy of the catalog, on its
own claim. It wrote the gap correctly before plan 78. The gap was in the
wrong place, on the media volume.

## The design

### The list on the bus

The close container of a library `Job` publishes the gap of each worker
fact that the `Library` turns on, after every phase has ended. Each
video is one retained message:

    <base>/libraries/<namespace>/<library>/missing/<fact>/<run>/<index>

`<run>` is the library `Job`'s name, so the lists of two runs never
mix. `<index>` counts from 0, in the order the gap query returns the
videos. The payload is one JSON object with the video's path under the
root, its size, and its length in milliseconds. The worker checks the
size against the file before it opens it, and `trickplay` reads the
length to plan its thumbnails. The close container publishes the count
last, as a retained message on `…/missing/<fact>/<run>/count`.

The broker delivers one connection's messages in order, so a count
that reaches a subscriber proves that every item before it reached the
broker. The close container subscribes to its own count topic and waits
for the retained message to come back, bounded by the hand-off timeout,
before it writes its finished run row. A count that does not come back
is a failure in the run row, and the operator starts no worker for that
list.

No topic the operator or a screen subscribes to today reaches
`…/missing/…`, because every filter in `topics.go` names its levels with
`+`. The operator adds one filter for the count topics,
`<base>/libraries/+/+/missing/+/+/count`. A retained message on the bus
costs no etcd space, and a few thousand messages of about 200 bytes are
ordinary for Mosquitto.

The library `Job`'s containers hold no bus address today. The close
container takes `LIBRARY_BUS_ADDRESS`, as the reporter does, and the
facts to list, as it did before plan 78.

### The worker `Job`

The operator starts a worker of a fact when the library `Job` that
published the list has finished its enrich run, its count is above zero,
and no worker of the fact is unfinished. These are plan 78's rules, with
the published count in place of the work list.

The worker is an Indexed `Job` at every size:

- `completions` is the count.
- `parallelism` is `spec.<fact>.parallelism`, which now means how many
  videos run at once, from 1 to 16.
- `backoffLimitPerIndex` retries a video on its own, and a video that
  fails every retry fails its index alone.
- The annotation `library.liken.sh/work-list` names the run whose list
  the `Job` works.

Each pod holds no catalog. It reads `JOB_COMPLETION_INDEX`, subscribes
to its own item topic, takes the retained message, and disconnects. It
then does what a worker did with each video before: checks the file
against the size, runs the fact, writes the outputs and the ledger, and
asks the operator to rescan the video's title folder. Two pods can
write the ledger of one series folder at once. The update door
(`volumeupdate.go`) merges them, as it does for two clusters on one
volume. Each pod sends one rescan request, and the operator holds up to
64 paths for a `Library` and runs one full walk in their place past
that.

A pod whose item topic holds no message logs it and exits zero. The
broker keeps retained messages in memory (`persistence false`), so a
broker restart clears every list. The worker then ends quickly, its
videos stay in the gap, and the next library `Job` lists them again.

Every pod goes through the scheduler on its own, so the work flows to
whichever matching nodes are free: a GPU claim through dynamic resource
allocation, a node's taint, and the pod's resource requests all apply
as they do to any pod. `liken` publishes a render node for many claims
at once, so the GPUs do not cap the pods, and `parallelism` does.

The 24-hour deadline stays the `Job`'s. A list that is not done by then
ends Failed, and the indexes that did not run stay in the gap for the
next list.

### Clearing a list

The operator clears a run's list by publishing an empty retained payload
on each item topic and on the count topic. It does that when the
`Job` that worked the list finishes or is deleted, and when a newer list
of the same fact replaces a list that no worker took. On a restart, the
operator reads the count topics back from the broker and clears every
list it holds no reason to keep.

### What plan 78 loses

- The worker pod's catalog agent, its `emptyDir` or claim, and the sync
  target and timeout it waited on.
- `Catalog.spec.workers.storageClassName` and the `<catalog>-workers`
  claim. No cluster set the field.
- The split by hours (`workershare.go`). One video per index needs no
  split.

Plan 78's removal of the `.liken` directory at the library root stays,
and so does the rule that the root holds no video of its own.

### What a person sees

- `kubectl get pods -l library.liken.sh/worker=<fact>` lists one pod per
  video, each with its own log and its own `Event`s.
- `kubectl get job` shows the worker's completions out of its count, and
  `status.failedIndexes` names the videos that failed.
- `mosquitto_sub -v -t '<base>/libraries/<namespace>/<library>/missing/#'`
  lists the videos that wait.

The pattern fits any work that a complete source can list as items and
that each item's pod can do alone.

## What was set aside

- **A catalog copy in each worker pod (plan 78).** The copy needs a
  list of every writer's newest version to know it is complete, a full
  sync at each pod start, and memory for an agent.
- **The whole list as one retained message.** Every pod would read about
  a megabyte to use one line of it, and a person could not read one
  item on its own.
- **A queue in MQTT 5.** Shared subscriptions hand each message to one
  member of a group, and acknowledgements could hold a pod to one video
  at a time. Both operators' clients speak MQTT 3.1.1 at QoS 0, the
  broker would need persistence, and a shared subscription drops a
  message published before a member subscribes, while the library
  `Job` ends before the worker's pods start. Whether Mosquitto gives a
  message to a member with nothing in flight, and what it does with a
  dead member's unacknowledged messages, is not measured.
- **A `Job` for each video.** A series library would be thousands of
  `Job` objects at once, and Kubernetes has no limit on concurrent
  `Job`s. A `ResourceQuota` refuses a create where a queue would wait,
  and Kueue is another controller to run.

## The proof

The tests publish a list from the close container to an in-process
broker and read it back by index, check that the count comes last and
that the run row waits for it, build the Indexed `Job` from a count, run
a pod against a present and a missing item, and clear a list when its
`Job` ends and when a newer list replaces it.

The drill on `liken-1`: turn on `appearances` for `series` at a
parallelism of 4, and record each pod's time from creation to work, its
wall time per video, the `Job`'s progress, and that the list leaves the
bus when the `Job` ends.

## What the drill found

Before the drill, a throwaway test published a list of 3,000 videos to
`liken-1`'s Mosquitto 2.0.22 over a port-forward, read three indexes
back in sessions of their own, read an index past the list as absent,
and cleared the list, in 0.75 seconds in all. Mosquitto sent each
retained message before the answer to the ping that followed the
subscription, which is the order a pod's read depends on.

On `liken-1` on 2026-10-08, `appearances` turned on for `series` at a
parallelism of 4, on a `ResourceClaimTemplate` of the shared render
node. The close container of the walk published a list of 6,828
videos, and the operator started the worker after the walk's enrich
run.

The first run found a defect. The pass that started the worker also
cleared its list, because the sweep reads the Jobs from the start of
the pass, the new worker was not among them, and a rule cleared a list
once a worker had taken it. Every pod read its index as absent, logged
it, and exited zero within seconds. The `Job` was deleted, the rule was
removed, and the sweep now keeps a list that no `Job` names while it is
the newest work of its fact.

The second run, on the fixed build:

- Each pod started its work 6 seconds after the `Job` controller
  created it, and decoded on the render node.
- `liken-1` has 4 cores. Each pod requests 1 core and used 0.3 to 0.4,
  so three pods ran, and the fourth stayed Pending on CPU until one
  ended. The taint on `stick-1` kept every pod off it.
- With three pods on the one iGPU, each 47-minute episode took 11
  minutes 15 seconds, at about 680 MiB.
- Seven episodes were done after 27 minutes. Their rescans waited
  behind the hourly walk that had started meanwhile, and 20 minutes
  into that walk the catalog held the found attempts of the first
  three.
- The hourly walk of `series` that ran beside the three decodes took
  17 minutes 56 seconds for 177 folders, where the walk before the
  worker took about 2 minutes. The decodes and the walk read one
  volume, and the walk waited 4 minutes for CPU before it started.
- Deleting the worker and turning the fact off cleared the list from
  the bus within a minute. The walk that was running when the fact went
  off still published a list of 6,825 videos, because its close
  container was built with the fact on, and the next pass cleared that
  list too, with no worker started.

The backlog of 6,828 episodes is not the testbed's to work, so the
drill stopped there.
