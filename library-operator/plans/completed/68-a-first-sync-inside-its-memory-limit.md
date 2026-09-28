# 68, A first sync inside its memory limit

Built on 2026-09-27 and measured on a workstation against the Corrosion
image of release 2026.09.27-004. It closes the open problem "Ingest
memory and the restart that returns it". Every catalog agent now holds
at most 1,000 changesets in its apply queue, and its image runs glibc
with two malloc arenas. A first sync of a synthetic catalog of 600,000
rows peaked at 338 MiB, where it peaked at 1,029 MiB with neither
setting. The run on a home cluster that confirms the fix is still owed.

## The problem

On 2026-09-27 the kernel killed the catalog agent of a series library's
gaps `Job` on a home cluster. The agent started on an empty claim at
21:16:16, received its first changes between 21:16:30 and 21:16:40,
and was killed at 21:17:27 at its `1Gi` limit, with 1,044,992 kB of anonymous memory. Its restart
continued the sync from the `state.db` on the claim, but the `trickplay`
phase lost the agent's API while the agent restarted, and the `Job`
failed.

The agent's own log and the memory history give the cause:

| Time | Working set | Log |
|---|---|---|
| 21:16:40 | 216 MiB | first changes arrive |
| 21:16:45 | | `dropped old change from queue`, the first of more than 10,000 drops |
| 21:16:50 | 408 MiB | `commiting transaction took too long` |
| 21:17:10 | 641 MiB | |
| 21:17:20 | 844 MiB | |
| 21:17:27 | | killed at `1Gi` |

A drop happens only when the apply queue in Corrosion's
`handle_changes` holds `perf.processing_queue_len` changesets, 20,000 by
default. So the queue was full within 15 s of the first change, and it
stayed full while the memory grew by another 600 MiB. The queue counts
changesets and not bytes. A changeset from a sync is about 8 KiB on the
wire, and each one in the queue added about 100 KB to the agent's
memory in the drill below, so 20,000 of them can hold about 2 GB. The
log does not show the size of each changeset. The growth at a full
queue fits small early changesets that larger ones replace, but that
part is an inference. Over the two days before, twelve library `Job` agents on the
same cluster peaked above 500 MiB, up to 963 MiB.

So the limit was not set below what the work needs. The queue grows to
whatever memory the agent has, because SQLite applies the changes more
slowly than the peers send them. A larger limit moves the kill and does
not remove it.

The open problem also named the memory an agent keeps after a first
sync: 294 to 354 MB against 74 MB after a restart. The drill below
shows that part of it is freed memory that glibc keeps in its arenas.

## The design

Two settings in the Corrosion image this repository builds. The catalog
agents take both: the agents of the catalog pods, the screens, and the
library `Job`s. The progress agents read `progress.toml`, so they take
only the second.

* `corrosion/config.toml` sets `[perf] processing_queue_len = 1000`.
  When the queue is full, Corrosion drops the oldest changeset and
  removes it from its cache of seen changes, so a later sync requests
  it again. A smaller queue costs a second fetch and loses no change.
  The same count bounds the queue of broadcasts the agent sends. Above
  it, the agent drops the broadcast it has sent most often, and a peer
  receives that change by sync.
* `corrosion/Dockerfile` sets `MALLOC_ARENA_MAX=2`. SQLite allocates
  through glibc's `malloc` from many threads, and by default glibc makes
  up to eight arenas per core. Memory freed in one arena is not used by
  a thread attached to another. The agent's Rust allocations use
  jemalloc and do not read this variable.

The memory limits do not change. The `Job` agent keeps `1Gi` and the
other agents keep `512Mi` until a first sync on a home cluster measures
the peak under the new settings.

## What was set aside

* **A higher limit alone.** The queue fills any limit, so the kill only
  moves.
* **jemalloc's decay settings.** `_RJEM_MALLOC_CONF` with a background
  thread and a one-second dirty decay lowered the peak by 26 MiB, from
  537 to 511 MiB. Two glibc arenas lowered it by 199 MiB, so most of the
  memory the agent keeps is in glibc and not in jemalloc.
* **A patch to Corrosion that bounds the queue in bytes.** It is the
  better fix, and it belongs in the fork. The count bound is a setting
  that needs no patch.

## How the work is proved

The drill ran on a workstation with two agents from the image of
release 2026.09.27-004 in `docker`. The first agent held a synthetic
catalog: 600,000 rows in `attempts`, written through its API in 600
transactions of 1,000 rows. The second started on an empty directory,
with two CPUs, and synced the whole catalog. A script read the second
agent's anonymous memory from its cgroup every second, and its queue
from `corro_agent_changesets_in_queue`.

| Settings | Peak | After the sync | Largest queue | Drops | Sync time |
|---|---|---|---|---|---|
| Corrosion's defaults | 1,029 MiB | 567 MiB | 7,979 changesets | 0 | 412 s |
| queue of 1,000 | 537 MiB | 537 MiB | 1,000 | 11,350 | 392 s |
| queue of 1,000, jemalloc decay | 511 MiB | 511 MiB | 1,000 | 12,389 | 434 s |
| queue of 1,000, two arenas | 338 MiB | 338 MiB | 1,000 | 15,684 | 453 s |

At Corrosion's defaults the anonymous memory followed the queue: from
582 MiB with 3,501 changesets waiting to 921 MiB with 6,846, about
100 KB for each changeset added. The
workstation applied faster than the home cluster's node, so its queue
peaked below the 20,000 bound and dropped nothing. With the queue at
1,000, the memory still grew after the queue drained: from 350 MiB at
244,000 rows to 537 MiB at the end. With two arenas it grew from 156 MiB
at 262,000 rows to 338 MiB. So two arenas lower the level by about
190 MiB, and a growth of about 180 MiB while the buffered changes are
applied remains in both runs. Its source is not measured. Each setting
ran once, and the sync times range from 392 s to 453 s. The two-arena
run took 41 s longer than the default, and one run each does not say
whether the arenas slow the sync.

A second drill checked the broadcast side of the queue bound. A writer
agent with a queue of 1,000 and a peer with the defaults started
together, and the writer then took 100,000 rows through its API in 200
transactions of 500 rows, the scanner's batch size. The writer dropped
no broadcast, and the peer held every row 12.4 s after the last write,
the same as with a writer at the defaults.

Still owed: the first `Job` of a `Library` on a node of the home cluster
with an empty catalog claim, after the release that carries this
change. The drill reads the agent's peak from
`container_memory_working_set_bytes` and its queue from
`corro_agent_changesets_in_queue`, and it confirms the kernel kills no
agent. The same cluster's kernel also killed agents at `512Mi` in the
two days before. The drill checks whether the queue bound ends those
kills too.
