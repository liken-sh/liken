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
