# The catalog pod

Plan 28. One standing pod per namespace holds the durable catalog and
reports what it holds, every scanner becomes a `Job` with an agent of
its own, and a `Job` knows it is done when the standing pod says so.
This is the first build of [plan 27](../27-enrichment.md), and it changes
what plans 02, 04, 15, 19, and 21 built.

## The problem

Each `Library` runs a scanner pod that never exits. It walks the volume
once, then waits for webhooks and a timer. The pod stays up for one
reason: its Corrosion sidecar holds a copy of the catalog on a
`Library`-owned claim, and if every pod left, so would the catalog. The
walk itself has nothing to do all day.

Plan 27 adds enrichers, and each one is a short piece of work: read a
gap, ask a provider, write a file. A `Job` per piece of work is the
Kubernetes shape for it. Two facts about Corrosion set the terms.
Its HTTP API answers on loopback only, on purpose, because it is an
unauthenticated read-and-write surface and the cluster's network
enforces no policy. So a `Job` that writes the catalog runs an agent
of its own. And an agent that receives `SIGTERM` drops whatever
broadcasts it still holds. Its broadcast loop ends on the stop signal
with no drain, and its peers fill a gap only by pulling from the
agent that has the rows. So a `Job` whose agent exits right after its
last write can leave those rows on its own claim until its next run.

## The contract

- **The `Catalog` owns one pod.** It holds a Corrosion agent on the
  namespace's one durable claim, sized from `Catalog.spec.storage` or
  bound to an existing claim named in `spec.storage.claimName`. It is
  the standing member of the gossip cluster. It answers on no port.
- **The catalog pod reports.** Beside the agent, a container from the
  operator's image reads the loopback API and publishes over the bus:
  the counts per library that the scanner publishes today, the `runs`
  table below, and later the gaps the enrichers act on. It takes the
  agent's subscription stream for `runs`, and the per-table update
  stream for every other table, republishing at most once a second
  while rows move, so a change reaches the bus within the stream's
  latency and it polls nothing. It is the one reporter in the
  namespace.
- **The `runs` table.** A replicated table with one row per library
  and worker. A worker is `scan`, `cleanup`, or a concern from plan 27.
  The row holds the `Job`'s name and the time it finished, and a `Job`
  writes it as its last write.

  ```sql
  CREATE TABLE runs (
      library TEXT NOT NULL DEFAULT '',
      worker TEXT NOT NULL DEFAULT '',
      job TEXT NOT NULL DEFAULT '',
      finished INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (library, worker)
  );
  ```

- **A `Job` exits when the standing pod holds its rows.** After its
  last write, the `Job`'s main container reads its own agent's item
  and file counts for its library, subscribes to the reporter's
  message on the bus, and waits until the `runs` row for its worker
  names its own `Job` and the report's counts equal its own. The run
  alone is not proof: Corrosion applies a version when it arrives and
  fills the gaps behind it by pulling from the source, so the last row
  can land while thousands before it are still in flight. Then it
  exits, and the kubelet stops the sidecar. The wait has a timeout,
  and a `Job` that times out fails, so the rows stay on its claim and
  the next run carries them. A folder scan writes a `rescan` row, so
  the full walk's numbers stand beside it.
- **A scan is a `Job`.** It runs the scanner beside a Corrosion agent,
  as a native sidecar with the exec probe from plan 04, on the
  `Library`'s existing catalog claim. The claim keeps the agent's
  actor id and its rows between runs, so a run syncs a delta and a
  fresh `emptyDir` is never used. `ReadWriteOnce` on the claim is what
  serializes one library's scans. The walk, the prune, and the local
  `seen` table are the same code. The scanner publishes no report of
  its own.
- **The operator creates every `Job`.** A webhook creates a scan `Job`
  for one folder. A `CronJob` per `Library` runs the full walk on
  `spec.scan.schedule`, a cron expression that defaults to once an
  hour. A folder `Job` may run beside a full walk only when a second
  claim exists, so it does not: the operator holds a folder scan while
  a full walk runs and starts it after.
- **The webhook is on the operator.** One `Service` over the operator
  in its own namespace, with one path per library,
  `/webhook/<namespace>/<name>`, and `status.webhook` names that URL.
  The per-`Library` webhook `Service` goes away with the scanner pod.
- **`Library` status gains `runs`.** One entry per worker with the
  `Job`'s name and the time, from the reporter's message. `phase`
  reads `Scanning` while a scan `Job` runs, as it does today.
- **Departure uses the same door.** A deleting `Library`'s cleanup
  runs as a `Job` on the library's claim, deletes the rows, writes its
  `runs` row, and waits for the standing pod to echo it. The
  survivor-report convergence from plan 21 goes away, because the
  standing pod is the one survivor that matters and it never exits.
- **Screens do not change.** A screen pod keeps its own agent on an
  `emptyDir` and gossips with the standing pod. A screen reads its own
  file. The catalog `Service` and its `EndpointSlice` stay as they are,
  and the slice now lists the catalog pod, the running `Job` pods, and
  the screen pods, by the same label.

## Failure

When the catalog pod is down, a `Job` still writes to its own claim
and then waits for an echo that does not come, so it times out and
fails, and Kubernetes retries it. Its rows are on its claim and go out
on the retry. Screens keep reading their own copies. When the catalog
pod's node is down and its claim is `ReadWriteOnce` there, the pod
waits for the node. That is the one pod to look at when nothing
reports.

## The change on a running cluster

No fresh database. The `Catalog`'s new claim starts empty and pulls
every row from the first scan `Job`'s claim, which holds the whole
catalog already. The roll order is the operator, then the catalog pod
comes up, then the scanner pods are deleted and the `CronJob`s take
their place. The `Library`-owned claims stay and the `Job`s reuse
them.

## Proof

On `liken-1`: the roll, a full walk of the movies and series libraries
through `Job`s, a folder scan from a webhook, a screen that comes up
against the catalog pod, and a kill of the catalog pod while a walk
runs. Recorded: the time from a `Job`'s start to its first write, the
time from its last write to the echo, the time to the first full
report against the number plan 04 recorded, the catalog pod's
resident memory after the walk and after a restart, and what the
killed walk left in the catalog and on the `Job`'s claim.

### The drill, 2026-09-02

Built in releases 2026.09.02-004 to -008 and drilled on `liken-1` the
same day, against the movies and series libraries.

- The roll: the catalog pod Ready on a fresh claim in 15 s, and both
  libraries Ready. The old scanner pods were deleted by hand, because
  the operator no longer lists them and they hold the claims.
- Full walks as `Job`s: movies 12 to 19 s and series 24 to 28 s, the
  same as plan 04's scanner. The echo came within one second of the
  last write. The sidecar's exit added 8 to 35 s to every `Job`, which
  is the slow-shutdown open problem.
- The webhook: post to `Job` in one second, pod up in 13 s, rescan in
  0.2 s, echo under a second.
- The catalog pod killed mid-walk came back in 7 s, both `Job`s
  completed, and every row was in place. The pod reads 171 MiB after
  the ingest and 141 MiB after a restart, which took 22 s to Ready.
- A departure released 27 s after the delete.

The drill found four gaps, each fixed in the release named. The `runs`
row alone did not prove the rows arrived: 266 of 1415 titles were
still in flight when the echo came, so the echo now carries the
counts (-005). RBAC granted no `update` on `CronJob`s, so every pass
failed on the image bump (-006). A new `Library` got no walk until its
`CronJob`'s hour, and the cleanup container had no bus address, so a
departure cycled forever (-007). The first-walk rule read a `Job`'s
pods and not its verdict, and started a second walk between a failed
pod and its retry (-008). One finding stays open: a fresh agent's
first version reaches the catalog pod minutes after the rest, the
[open problem](../open-problems/a-fresh-agents-first-version-arrives-late.md)
that costs a new `Library` one echo timeout on its first walk.

## What is set aside

A `Job` with an `emptyDir`. A write near the end of the run is lost
for good, and every run joins the cluster as a new member with a new
actor id.

The agent's HTTP API on a `Service`, with or without a token in
front. It puts an unauthenticated write surface on the network, or
adds a proxy to guard it, when a `Job` with its own agent needs
neither.

A `Job` that watches its `Library` for the echo. It needs a token and
RBAC in every `Job`, and it reads a status the operator derived from
the same bus message the `Job` can read itself.

A drain of the broadcast queue on shutdown, inside Corrosion. Its
source marks the place with "nothing to do here, yet!", and a patch
there is one to offer upstream. It changes nothing here until a
release we pin carries it.

## What is not decided

The echo timeout. The grace period the catalog pod needs, given the
slow shutdown problem. Whether the ingest peak on the standing pod
needs the restart trick plan 06 uses on screens. Whether the
`CronJob`'s hourly default is right for a library with a webhook and
whether a library with none wants it shorter.
