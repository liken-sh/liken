# Ingest memory and the restart that returns it

[Plan 32](../completed/32-screen-catalog-persistence.md) solves the
screen half of this problem with a `PersistentVolumeClaim` per screen.
A screen then does the first sync once per claim, where before it did
it once per pod start. The catalog pod's own peak is still open. This
note stays until that plan is built.

When a Corrosion agent ingests a large catalog for the first time, its
memory use rises to a high peak and stays there. The proof of concept measured a fresh node syncing
105,000 rows at 294 to 354 MB resident after the sync, and the same
agent restarted on the same files at 74 MB. The difference is memory the
process keeps for its whole life.

Plan 06 uses the simple fix: a screen's sidecar restarts once after
its first full sync. That workaround has two costs. The media browser's
stream drops and reconnects during the restart, and a builder has to
remember the rule. Two better fixes are possible, and neither is
measured. A
screen's pod could start with the catalog already on its disk, on the
machine's local storage across pod restarts rather than an `emptyDir`,
so only the first boot ingests. Or the agent's allocator could return
the memory. That depends on Corrosion's build, its allocator, or a
patch.

The design's memory budget uses the at-rest number. This problem is
about the peak.

## The first sync of a Library's `Job`

A Library's `Job` runs the same agent, with the same limit of 512Mi,
on the Library's catalog claim. The claim keeps the agent's state
between `Job`s, so only a `Job` on a new, empty claim does the first
sync. On 2026-09-26 each catalog claim was made again as
`ReadWriteOncePod`, and each first walk on the new claim synced the
whole namespace.

- On `liken-1`, the first walk of the movies peaked at 384Mi for the
  agent and did not restart it.
- On the house cluster, the agent of the first `series` walk was
  OOM-killed three times, and the agent of the first `movies` walk
  twice. Each restart continued the sync from the `state.db` on the
  claim, and both walks completed: the movies in 4 min 21 s. After the
  sync the agent used about 190Mi.

So a first sync on a claim of the house cluster needs more than 512Mi,
and it completes only because the claim keeps what each restart
synced. The limit was not raised. A larger limit for the agent of a
`Job`, or the same allocator fix as above, would remove the restarts.
