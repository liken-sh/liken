# 87, The idle cost of the catalog agents

This plan, written on 2026-11-06, finds what the Corrosion agents of a
namespace spend while nothing changes, and why each catalog store is
about 850 MB, and lowers what it can. The catalog agents now wait up
to 60 seconds between two syncs, where Corrosion's default waits up to
15, and they apply a small batch of peer changes after 1,000 ms, where
they waited 100 ms. New catalog claims are `2Gi` by default, where they
were `1Gi`. The progress agents are unchanged, so a position that a
screen reports once a second replicates as before. With 101 actors in
the catalog, an idle catalog agent on a workstation went from 3.5m of
CPU and 0.55 kB/s sent to 1.5m and 0.23 kB/s.

Status on 2026-11-07: built. The measurements are from three agents
on a workstation and from one read of the metrics and the change log
of a home cluster's agents. The drill on a home cluster after the
release that carries the change has not run.

## The problem

A survey of a home cluster over 5.5 minutes found that its 12
Corrosion agents used about 148m of CPU, 46% of all the `liken` pods,
and moved about 80 kB/s, 57% of the pods' traffic. The 12 agents are
three catalog copies, three progress copies, and two sidecars in each
of three media browser pods. A browser's catalog sidecar used 8m to
12m of CPU with no writes arriving. Each of the six catalog stores was
about 830 MiB, 81% to 84% of its `1Gi` claim, for a library of 1,509
films, 206 series, and 8,012 episodes. One catalog copy held 328 MiB
of memory, against 67 MiB to 88 MiB for the others. This plan does
not explain that difference.

The survey proposed three causes. Each one is checked below.

1. Every hourly walk upserts every row it reads with
   `ON CONFLICT ... DO UPDATE` and no `WHERE`, so each walk might
   replicate the whole catalog to every agent.
2. Every agent syncs with three peers on a backoff from 1 to 15
   seconds that never resets.
3. Every agent wakes ten times a second, because
   `apply_queue_timeout` is 100 ms.

## What the measurements found

### A walk that changes nothing replicates nothing

cr-sqlite compares each column's old and new value in its update
trigger, and it records a change only for a column whose value
differs. The test loaded the cr-sqlite build that the pinned Corrosion
image carries (`crates/corro-types/crsqlite-linux-x86_64.so` in the
fork at the image's commit) into SQLite 3.46, applied
`corrosion/schema/catalog.sql`, and ran the `movies` upsert of
`catalog.go`, which has no `WHERE`:

| Step | `crsql_db_version()` | Clock rows | Newest `col_version` |
|---|---|---|---|
| First insert | 1 | 15 | 1 |
| The same upsert again | 1 | 15 | 1 |
| The same upsert a third time | 1 | 15 | 1 |
| The upsert with a new `title` | 2 | 15 | 2, on `title` only |
| The same upsert with a `WHERE` guard | 2 | 15 | 2 |
| A new `title` with the `WHERE` guard | 3 | 15 | 3, on `title` only |
| `UPDATE movies SET title = title` | 3 | 15 | 3 |

SQLite still counts a row as affected when the `UPDATE` writes the
same values, so `rows_affected` is 1, but no clock row and no version
changes. Corrosion's `/v1/transactions` answers `"version": null` for
such a write and broadcasts nothing.

The same holds through the agents. Three agents held a synthetic
catalog of 937,675 rows, shaped like the home library (see below). A
second pass posted every row again, in the scanner's 500-statement
transactions. None of the 1,880 transactions made a version. Each of
the two peers received 0.02 MB in the 84 seconds of the pass, the same
as at rest. The writer's agent spent 41.6 s of CPU on the pass. With a
`WHERE excluded.x IS NOT x OR ...` guard on every upsert, it spent
40.9 s, so the guard saves nothing measurable, and the writers keep
their upserts.

On the home cluster, the change log of a catalog copy and its
`crsql_changes` show what the walks do write. In two hours, the copy
logged 19 versions from the series walk's actor that were large
enough to arrive in parts, with 16,195 changes, all at the first walk
after a release. The cells of that walk that the copy still holds are
16,436, and every one is `content_rating` or `tagline` of an episode
or a series: all 8,218 rows, two columns each. The release added those
columns, so that walk was the one-time fill of them. In the 2,000
versions of the same actor before that walk, the cells the copy still
holds from it are mostly `attempts.at` and `attempts.dataset_modified`,
about 1,300 of them, and 327 `tallies` rows. Those are real changes: a
fact's attempt time and the tallies of each run.

### What fills a store

cr-sqlite keeps one clock row for each column of each row that is
not in the key, with an index on the version, and Corrosion adds a
second index on the site and the version. So the clock tables are
about twice the size of the rows they track. The synthetic catalog used the home library's
item and file counts, and estimated the other tables per title. A
later read of `corro_db_table_rows_total` on a home copy found similar
counts: 208,858 `attempts`, 120,604 `contributor_ids`, 120,943
`contributor_aliases`, 78,085 `streams`, 74,389 `credits`, 58,149
`files`, and 50,836 `contributors`.

| Part of the writer's file | Size |
|---|---|
| cr-sqlite clock tables and their indexes | 443.5 MiB |
| The catalog's rows and indexes | 218.4 MiB |
| cr-sqlite key tables (`__crsql_pks`) | 77.9 MiB |
| Corrosion's bookkeeping | 0.1 MiB |
| Freelist | 0.0 MiB |
| The whole file | 740.8 MiB |

The largest single objects are the clock tables of `files` and
`attempts`, 57 MiB each, and of `streams`, 48.5 MiB. A home copy's
metrics read `corro_db_size` 843,931,648 bytes, `corro_db_wal_size`
5,994,632 bytes, and `corro_db_freelist_count` 2,351 pages of 4 KiB,
so the home stores at rest are rows and clocks, and the WAL and the
freelist are small.

A copy that receives a large write holds it in
`__corro_buffered_changes` until every part of the version has
arrived, and then applies it. While the seed ran, each peer's file
reached 1,314 MiB, and one read found 588 MiB of it in that table.
When the peer had applied the seed, the table was empty and 573 MiB
were on the freelist. Corrosion runs `incremental_vacuum` every five minutes until
fewer than 10,000 pages are free, so the file returns to its size at
rest. A first sync onto an empty directory peaked at 1,414 MiB on
disk, with the WAL. A `1Gi` claim does not hold that, so the default
is now `2Gi`.

The per-node driver reports a copy's use as the sum of its files'
sizes, so the survey's 830 MiB includes the WAL and the shared-memory
file.

### The idle cost is syncs, times the actors

A sync sends the peer the newest version the agent holds from every
actor that ever wrote the catalog, and the peer answers with its own
list. Corrosion keeps an entry for every actor and removes none. Every
worker pod on an `emptyDir` is a new actor (plan 78), and so is every
other agent that starts on an empty directory and writes. So the cost
of a sync that finds nothing grows with the number of pods that ever
wrote. The wait between syncs starts at one second, doubles after each
sync, and stays at the ceiling, which is 15 seconds by default. The
backoff never resets, but at the ceiling that changes nothing.

The test ran three agents from the pinned image with this
repository's `config.toml`, on a docker bridge network on a
workstation with 22 cores. The CPU is the container's cgroup
`usage_usec`, and the traffic is the bytes each agent's network
namespace sent. Each row is one run, per agent, after the backoff
reached its ceiling.

| Catalog | Actors that wrote | Sync ceiling | CPU | Sent |
|---|---|---|---|---|
| Empty | none | 15 s | 2.2m | 0.19 kB/s |
| 937,675 rows | 1 | 15 s | 2.6m | 0.20 kB/s |
| 937,675 rows | 101 | 15 s | 3.5m | 0.55 kB/s |
| 937,675 rows | 101 | 60 s | 2.2m | 0.23 kB/s |
| 937,675 rows | 101 | 120 s | 2.1m | 0.17 kB/s |

The 100 added actors were short-lived agents on empty directories, like
worker pods, that each wrote one `runs` row and left. With them, the
agents at the 15-second ceiling sent 0.35 kB/s more and used 0.9m more
CPU each. At 60 seconds most of that cost is gone.

On the home cluster, a catalog copy and a screen's catalog sidecar
each sent about 2.1 kB/s over two hours: 0.83 to 0.89 kB/s of sync,
1.24 kB/s of broadcast, and 0.05 kB/s of membership gossip. The two
hours held the fill of the card columns above, so the broadcast share
was larger than in an hour of ordinary walks. The progress agents
open no metrics port, so their share of the survey's traffic is not
measured. The pod traffic the survey read also counts the other
containers of each pod, such as the browser and the confirmer.

### The 100 ms tick

`apply_queue_timeout` sets two timers in `handle_changes`. One is the
wait before a batch of peer changes smaller than the threshold is
applied, and the agent arms it again each time it ends with an empty
queue. The other sets four gauges and trims a cache. Both run while
nothing arrives. The same three agents, at the 60-second sync ceiling
and with 101 actors, each ran one 300-second sample at each value:

| `apply_queue_timeout` | CPU | Sent | A 1 Hz write reaches a peer |
|---|---|---|---|
| 100 ms | 2.2m | 0.23 kB/s | 58 ms median, 117 ms at most |
| 1,000 ms | 1.5m | 0.23 kB/s | 999 ms median, 1,004 ms at most |

The writer updated one `runs` row through the first agent once a
second for 120 seconds, and polled the second agent every 5 ms for the
new value. The wait before a small batch sets the lag in both rows,
so a catalog write now lands on a peer about a second after it
arrives. A batch that reaches the agent's threshold, 90 changes at the
smallest batch size, is applied at once, so a walk transaction that
changes its rows does not wait.

The progress agents keep 100 ms. The same test on three progress
agents with `progress.toml` and the `plays` update of
`progressstore.go` measured 2.9m and 0.20 kB/s per agent at rest, and a
position reached a peer in 53 ms at the median and 105 ms at most. That
configuration is the one the progress agents run before and after this
plan.

### The first sync

A longer ceiling changes when a new agent's later syncs come, and
every first sync starts at one second. A fourth agent, limited to two
CPUs, started on an empty directory and synced the 937,675 rows from
the three agents. The sync was complete when it held as many rows as
the first agent in every table.

| The new agent's ceiling | Rows from 0 to all | The first rows arrived |
|---|---|---|
| 15 s, first run | 151 s | before 31 s |
| 15 s, second run | 209 s | between 30 s and 60 s |
| 60 s | 237 s | between 60 s and 90 s |
| 120 s | 212 s | between 30 s and 60 s |

Every run after the first reused the address of the agent that had
just left, and in each of them no row arrived in the first 30 seconds.
From the first rows to the end, each run took 140 to 170 seconds. So
the spread is in when the sync starts, and the ceiling does not show
in the time. The 60-second run logged two changes dropped from the
apply queue and fetched again, and the others none.

## The design

- `corrosion/config.toml` sets `[perf] max_sync_backoff = 60` and
  `apply_queue_timeout = 1000`. Every catalog agent reads it: the
  durable copies, the screens' sidecars, and the agents of the
  `Library` `Jobs` and the workers.
- `corrosion/progress.toml` keeps Corrosion's default sync ceiling of
  15 seconds and `apply_queue_timeout = 100`. No setting of the
  progress agents changes.
- The default of `spec.storage.size` is `2Gi`. A `Catalog` that names
  a size keeps it, and a bound claim keeps its size, because a bound
  claim's spec is immutable. The progress claim takes `storage.size`
  when `spec.progress.size` is empty, so it is `2Gi` by default too.

A broadcast still carries every write as it happens, and a catalog
agent applies a small one about a second after it arrives, where it
applied it in about a tenth of a second. A write that a peer missed
reaches it at the peer's next sync, which is now at most a minute away
instead of 15 seconds. A sync only pulls: the agent that starts it
asks its peers for what it lacks. So a durable copy that missed an
earlier version of a `Job`'s run fills the gap at its own next sync.
The `Job` waits for that copy's confirmation for at most two minutes,
`HANDOFF_TIMEOUT`, and fails and runs again after it. At a ceiling of
60 seconds the copy syncs at least once in that wait, and it puts the
peers it lacks versions from first. At 15 seconds it synced about
eight times. A broadcast is missed when an agent's apply queue is full
or a peer is unreachable.

## What was set aside

- **`WHERE` guards on the upserts, or skipping unchanged files.** A
  write of the same values makes no version and no broadcast, so the
  guards save only the writer's CPU, and they saved 0.7 s of 41.6 s.
- **A ceiling of 120 seconds.** It saved 0.06 kB/s and 0.1m more per
  agent at 101 actors, and doubled the delay of a missed write.
- **A lower `wal_threshold_mb`.** Corrosion truncates the WAL only
  above 5 GiB, but SQLite resets it after each checkpoint, and the home
  copies' WALs were 6 MB. A forced `TRUNCATE` checkpoint holds the
  writer lock while it waits for readers, for up to 30 seconds.
- **Fewer clock rows.** A table with fewer columns has fewer clock
  rows: `files` and `streams` each have 20 columns that are not in the
  key, so each row of either is 20 clock rows. Folding the
  columns that no query filters on into one JSON column would shrink
  the clocks, but Corrosion cannot drop a column, so it is a new table
  and a migration of every row. That belongs in its own plan.

## Changes for the fork

Three changes to Corrosion would lower the cost further, and each
belongs in `liken-sh/corrosion`, and upstream:

- **Give the gauges their own period.** The timer that sets the
  queue's gauges, and the wait that re-arms on an empty queue, both
  take `apply_queue_timeout`. A gauge period of its own, and a wait
  armed only when a change arrives, would give the progress agents the
  idle saving of the 1,000 ms setting and keep their 100 ms apply.
- **Reset the backoff when a sync finds work.** A sync that receives
  changes, or leaves versions still needed, would start the backoff
  again at its minimum, and an idle agent would grow it to a higher
  ceiling, such as five minutes. A new agent would then catch up
  faster than today, and an idle cluster would sync less.
- **Forget actors that are gone.** An actor that has left the cluster,
  whose versions every member holds, needs no entry in the list a sync
  sends. Corrosion has no step that removes one, so the list grows
  with every worker pod.

## How the work is proved

Every local run used the pinned image,
`ghcr.io/liken-sh/corrosion:2026.10.31-001-dev-007-5a95a51c`, with
`MALLOC_ARENA_MAX=2` as the image sets it, and this repository's
`config.toml` or `progress.toml` with only the bootstrap list changed
to the three agents' addresses. The agents ran on one docker bridge
network on a workstation with 22 cores, so the traffic between them
crossed no physical link. Each number in the tables above is one run.
The scripts that ran them were throwaway: a seeder that writes the
synthetic catalog through `/v1/transactions` in the scanner's batch
size, a script that starts short-lived agents as new actors, and a
writer that updates one row a second and polls a peer for it.

The home cluster was read and not changed: its `Library` status for
the item and file counts, one scrape of the metrics port of a durable
catalog copy and of a screen's catalog sidecar through
`kubectl port-forward`, the catalog copy's log, and three read-only
queries of `crsql_changes` through the same copy's `/v1/queries`.

Still owed, after the release that carries this change: on the home
cluster, `corro_transport_tx_bytes_v2_total{traffic="sync"}` and the
CPU of each catalog agent over an hour with no walk, against the
numbers above. Before the change, both scraped agents read
`corro_sync_client_needed_v2` 0 and emitted no `corro_db_gaps_sum`,
which Corrosion writes only when a gap exists. Over the same hour both
should stay so, and no `Job` should fail on `HANDOFF_TIMEOUT`.
