# One Job for each Library

Plan 57. Each `Library` runs its work in one `Job` shape, on one catalog
claim, and only one `Job` of a `Library` runs at a time. The `Job` does
the walk, every enricher fact, trickplay, and the trailer files. Each
phase is a regular container, and all of them start together. A phase
works on each title as soon as the phases before it have written that
title's rows. It does not wait for those phases to finish every title.
The Corrosion agent is the only init container, because Kubernetes runs
a native sidecar as an init container.

This plan replaces the 2026-09-11 stub that [plan
34](completed/34-every-fact-writes-its-rows.md) split off. The stub ran
the enricher's phases at the same time. This version keeps that goal and
also puts the scan, trickplay, and trailers `Job`s into the same `Job`.
The design came from the 2026-09-25 review of [plan
33](33-the-imdb-datasets.md).

## The problem

### Four Jobs and four catalog copies for each Library

A `Library` runs four kinds of worker `Job`, and each kind has its own
claim and its own Corrosion agent:

| Worker | Started by | Catalog claim |
| --- | --- | --- |
| scan | the `CronJob` walk, a webhook's folder, a requested walk | `<library>-catalog` |
| enrich | an open gap after a scan, or a `spec.refresh` time | `<library>-enrich-catalog` |
| trickplay | an open gap after a walk, or a webhook chain | `<library>-trickplay-catalog` |
| trailers | an open gap after a walk | `<library>-trailers-catalog` |

Each claim has a full copy of the namespace's catalog. The separate
claims exist so that one kind of `Job` never waits for the claim that
another kind uses (`enrichjob.go:25`, `trickplayjob.go:26`,
`trailersjob.go:26`).

### The phases wait for each other

The enricher `Job` runs probe, arrival, identity, nfo, art, trailer,
marks, and contributors as init containers, in that order. Kubernetes
starts an init container only after the one before it exits. Most of
these phases do not need the phase before them:

| Phase | What it needs | Why it waits today |
| --- | --- | --- |
| probe | the file rows of the walk | it is first |
| arrival | the file rows of the walk | the probe writes the run's start mark |
| identity | the file rows of the walk | it follows arrival in the list |
| nfo | the ids from identity | its facts ask a provider by id |
| art, trailer | the ids from identity | they follow nfo in the list |
| marks | the ids, and the length the probe measured | it follows art in the list |
| contributors | the people that the credits fact creates | it follows marks in the list |

So a run of two thousand titles finishes the art of the first title
only after nfo has finished the last title.

### A webhook starts a chain of Jobs

A webhook for one folder starts a scan `Job`. When the scan ends, an
operator pass starts an enrich `Job`, and a trickplay `Job` if trickplay
is on. When those end, a pass starts a rescan `Job` of the same folder.
Each `Job` starts an agent, syncs the changes since its last run, and
confirms its runs row through a catalog pod (`handoff.go`). A new movie
pays that cost three or four times before it has its art and its tiles.

The rescan does little work now, because plan 34 made every fact write
its own rows. It still does three things:

- It sets `probed` on each file. The probe writes the codecs, size, and
  duration rows, but not `probed` (`factrowscatalog.go:27`).
- It derives the `Library`'s set rows again (`scan.go:651`).
- Its `rescan` runs row starts the next whole-library enricher when a
  gap is open (`enrichschedule.go:288`).

### Two agents can open one database

`ReadWriteOnce` limits a volume to one node, not to one pod. Kubernetes
applies the limit when it attaches the volume to a node, and every pod
on that node can mount it. A per-node claim is `ReadWriteMany`. The
agent mounts the whole claim at `/var/lib/corrosion`, where `state.db`
and `admin.sock` are (`pod.go:267`, `corrosion/config.toml:15`). So two
`Job`s of one claim on one node run two agents on one database.

No gate stops these cases:

- Two webhook folder scans of one `Library` run at once, because the
  operator creates one `Job` for each held path in the same pass
  (`scanjob.go:223`).
- The `CronJob` walk starts during a chain's rescan. Both use
  `<library>-catalog`, and `Forbid` stops only an overlap of the
  `CronJob`'s own `Job`s.
- A chain's trickplay `Job` runs beside the standing trickplay `Job`.
  The operator creates the chain's `Job` with no check
  (`enrichschedule.go:173`).

Three comments say that a `ReadWriteOnce` claim admits one `Job` at a
time (`scanjob.go:7`, `pod.go:170`, `cleanupjob.go:75`). That is true
only for pods on different nodes.

## The design

### The Job

```yaml
kind: Job
spec:
  template:
    spec:
      initContainers:
        - name: corrosion      # native sidecar, restartPolicy: Always
      containers:
        - name: scan           # walk mode only; the library volume read-only
        - name: probe
        - name: arrival
        - name: identity
        - name: nfo            # every nfo fact, the IMDb dataset facts included
        - name: art
        - name: trailer        # the list of trailer addresses
        - name: marks
        - name: contributors
        - name: trickplay
        - name: trailer-files  # the downloads
        - name: close          # writes the start mark, waits for every phase, hands off
      volumes:
        - name: catalog        # <library>-catalog, the one catalog claim
        - name: library        # the library volume
        - name: phases         # emptyDir: one mark for each phase, and the .nfo locks
```

A person's own scanner image, from `spec.<kind>.image`, runs in the
`scan` container. The operator's image runs in every other container.
The `scan` container mounts the library volume read-only, as the scan
`Job` does today, and the phases that write files mount it read-write.

### Two modes

- **Walk mode** runs the `scan` container, and the phases work on what
  the walk writes. The operator starts a walk on `spec.scan.schedule`,
  for a `spec.refresh` of `scan`, and for a webhook. A webhook's walk is
  limited to the folders that the webhook named.
- **Gap mode** has no `scan` container. The operator starts it when a
  gap is open and no walk is due, for example after a provider becomes
  `Ready` or after a `spec.refresh` of a fact.

In walk mode the operator cannot know the gaps before the walk, so the
`Job` includes every phase that the `Library`'s sources serve. In gap
mode the `Job` includes only the phases whose gaps the last report
counted. A phase with no work exits after one empty pass.

### When a phase ends

Each phase runs one loop. A pass reads its gap query from the local
catalog and works on every title it returns. Every fact already writes
its own rows as it writes its files (plan 34), so each pass finds the
titles that the phases before it finished since the last pass.

A phase ends when two things are true: every phase it depends on has
written its mark, and one pass after that found no gap. Then the phase
writes its own mark on the `phases` volume. The dependencies are the
"What it needs" column in the table under "The phases wait for each
other". In walk mode, `scan` is a dependency of every phase.

A phase waits for new rows on the agent's update stream, not on a
timer. The media browser reads the same stream today. The stream names
the primary keys that changed, and a phase runs its next pass when a
table that its gap query reads has changed. A phase that has no
dependency left runs its passes one after another and does not wait.

A phase that fails writes a failed mark. A phase that depends on it
finishes the gaps it already has and then ends. It does not wait for
rows that will not come.

### The .nfo file

Three phases write the `.nfo` file. The probe writes `<fileinfo>`
(`probe.go:124`), identity writes `<uniqueid>` (`identityrole.go:131`),
and nfo writes its element groups (`nfofactsrole.go:236`). Today they
run one after another, so no two edits of one `.nfo` file overlap.

Each edit reads the file, changes its own elements, and writes the
file with a rename. In the new `Job` the three can run at the same time,
so each edit takes an exclusive `flock` first. The lock is a file on
the `phases` volume, named by a hash of the `.nfo` path. The volume is
an `emptyDir`, so the lock is on a local filesystem, even when the
library volume is NFS. Only the containers of one `Job` edit the files,
because only one `Job` of a `Library` runs at a time.

### One Job at a time

The operator starts a `Job` for a `Library` only when no other `Job` of
that `Library` is unfinished. This gate replaces `libraryBusy`, the
`CronJob`'s `Forbid`, and the checks that each kind of `Job` makes for
itself.

- **The operator starts the walk.** The walk on `spec.scan.schedule`
  becomes a `Job` that the operator creates when the walk is due and
  the gate is open. The `CronJob` goes, because a `CronJob` cannot read
  the gate.
- **A webhook's folders wait for the running Job.** The operator keeps
  the folders that the webhooks named while a `Job` runs. The next `Job`
  walks every folder in that list. So five webhooks during one run start
  one `Job`, not five.
- **The catalog claim is `ReadWriteOncePod` where the class supports
  it.** The scheduler then keeps a second pod of the claim `Pending`
  until the first pod ends. The gate is still what prevents a second
  `Job`. `ReadWriteOncePod` is the guard for a fault in the gate. On a
  class with no support for it, the operator writes `ReadWriteOnce`, and
  the gate is the only guard.

`ReadWriteOncePod` on a per-node claim means one pod in the cluster,
not one pod on each node. That is the rule this plan needs. The next
`Job` can run on another node, and it uses that node's copy.

### Long phases stop at a time limit

One trickplay title decodes for minutes, with a limit of one hour for
each file (`trickplaysheets.go:41`). One trailer download takes up to
ten minutes (`trailerfetch.go:117`). A backlog of trickplay would hold
the `Job` for hours, and a webhook for a new movie would wait for all
of it.

So trickplay and the trailer files each have a time limit for one run.
When the limit ends, the phase finishes its current title, starts no
other title, and writes its mark. The rest stays as gaps, and the
operator starts the next `Job` in gap mode after the gate opens. A
webhook's folders wait at most one time limit plus one title.

The limit is a constant. The drill measures a value that keeps a
webhook's wait short and that still clears a backlog.

### The run and the hand-off

The `close` container starts with the other containers. It writes the
run's start mark first, so arrival no longer waits for the probe. It
then waits for the mark of every phase that the `Job` includes. When
every mark exists, it derives the `Library`'s set rows and writes the
runs row. Then it confirms the row through a catalog pod, as the
enricher's closing container does today (`handoff.go`).

The probe writes `probed` with the other rows of a file. The `close`
container derives the set rows, and the `Job` writes one runs row. So
the rescan stage has no work left, and it goes. Trickplay and the
trailer files get a runs row and a hand-off, which they do not have
today.

### Resources

Kubernetes schedules a pod on the sum of its regular containers'
requests. Today most phases are init containers, so the enricher pod
requests one phase's memory at a time. With twelve regular containers
at a request of 32Mi each, the pod requests about 384Mi, plus 64Mi for
the agent. The trickplay container also requests 500m of CPU.

The limits stay as they are for each container. The drill measures the
real peak of the `Job` on a 1 GB machine and decides whether the `Job`
needs a node affinity that keeps it off the screens.

### The claims

Each `Library` has one catalog claim, `<library>-catalog`. The operator
deletes `<library>-enrich-catalog`, `<library>-trickplay-catalog`, and
`<library>-trailers-catalog` in the release that builds this plan. The
cleanup `Job` that runs when a `Library` is deleted keeps its own shape,
and it uses the same claim behind the same gate.

## A change in per-node-csi-driver

The driver declares only `GET_VOLUME_STATS` (`node.go:69` in
`per-node-csi-driver`). It also declares
`SINGLE_NODE_MULTI_WRITER`, so the kubelet can send it the
single-writer access mode of a `ReadWriteOncePod` claim. The driver
accepts that mode and publishes the volume as it does for
`ReadWriteMany`.

## What this plan closes

- The deferred half of [plan 34](completed/34-every-fact-writes-its-rows.md),
  the phases that run at the same time.
- The two items that [plan 58](completed/58-trickplay-on-the-gpu.md)
  left open. A walk that opens only a trickplay gap now runs trickplay
  in the same `Job`. The standing trickplay `Job` goes, and with it the
  guard that only its name gave.
- The three cases where two agents open one database.
- A webhook chain of three or four `Job`s, which becomes one `Job`.

It also makes three open problems smaller, and it does not close
them. [A fresh agent's first version arrives
late](open-problems/a-fresh-agents-first-version-arrives-late.md) and
[slow agent shutdown](open-problems/slow-agent-shutdown.md) happen
less often, because a `Library` starts fewer agents. [Copies never give
space back](open-problems/copies-never-give-space-back.md) affects three
fewer copies for each `Library`.

## What was set aside

- **Each Job reads and writes the catalog through a catalog pod's
  API.** Every pod keeps its own copy of the catalog and reads and
  writes that copy. [Plan 28](completed/28-the-catalog-pod.md) set
  the API aside for the same reason: it puts a write surface with no
  authentication on the network.
- **One long-running pod for each namespace that runs every worker.**
  It has the fewest copies. But each run loses its own retries, logs,
  and memory limit, and the pod takes memory at all times.
- **Separate `Job`s for trickplay and the trailer files.** They were
  separate so that the enricher did not wait for them. The time limit
  gives the same result in one `Job`, and one `Job` needs one claim and
  one gate.
- **Init containers for the order of the phases.** An init container
  waits for the whole of the phase before it. The gap loop lets a phase
  work on each title as soon as that title is ready.
- **A timer between two empty passes,** which the stub proposed. The
  update stream starts a pass when a row changes and not otherwise.
- **One owner for the `.nfo` file that the other phases send their
  elements to.** It needs a channel between the containers. The lock
  needs one file on an `emptyDir`.

## What is not decided

- Whether the `local-path` provisioner on k3s accepts
  `ReadWriteOncePod`. Kubernetes supports that mode only on CSI
  volumes, and `local-path` is not a CSI driver. If it refuses the
  mode, the operator writes `ReadWriteOnce` on that class.
- The time limit for trickplay and the trailer files.
- Whether the Job succeeds when one phase fails. One option is that
  the `Job` succeeds when `close` writes the runs row, and the failed
  phase's gaps stay open for the next run. The other option is that
  the `Job` fails, and Kubernetes retries all of it.
- The cost of a subscription to the update stream in each phase.
  Eleven phases each hold one subscription to one agent.

## The proof

On `liken-1`, against the lab libraries:

- A full walk of a movie library shows every phase container running
  at the same time. The art of the first title is written before
  identity finishes the last title.
- A webhook for one new movie runs one `Job`. Record the time from the
  webhook to the art on the wall, and compare it with the chain today.
- Five webhooks during a trickplay backlog start one `Job` when the
  time limit ends, and that `Job` walks all five folders.
- Two `Job`s of one `Library` cannot run at the same time: create a
  second one by hand and confirm that it stays `Pending` on a
  per-node class.
- The peak memory of the `Job` on a 1 GB machine.
- The `probed` column and the set rows are correct with no rescan.
