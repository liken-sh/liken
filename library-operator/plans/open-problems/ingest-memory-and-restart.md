# Ingest memory and the restart that returns it

[Plan 32](../32-a-screen-keeps-its-catalog.md) answers the screen half
of this with a `PersistentVolumeClaim` per screen, so a screen pays the
first sync once per claim and not once per pod start. The catalog pod's
own peak is still open. The note stays until that plan is built.

A Corrosion agent that ingests a large catalog for the first time peaks
high and stays there. The proof of concept measured a fresh node syncing
105,000 rows at 294 to 354 MB resident after the sync, and the same
agent restarted on the same files at 74 MB. The difference is memory the
process keeps for its whole life.

Plan 06 takes the plain answer: a screen's sidecar restarts once after
its first full sync. That is a workaround with two costs: a window where
the media browser's stream drops and reconnects, and a rule a builder
has to remember. Two better answers are possible and unmeasured. A
screen's pod could start with the catalog already on its disk, on the
machine's local storage across pod restarts rather than an `emptyDir`,
so only the first boot ingests. Or the agent's allocator could return
the memory, which is a question for Corrosion's build, its allocator, or
a patch.

The at-rest number is the one the design budgets. The peak is what this
problem is about.
