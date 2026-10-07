# 77, A cleanup `Job` deletes its own run

This plan, written on 2026-10-07, answers the open problem "A cleanup
run outlives its `Library`". It chooses the first of that problem's
three options: the operator releases a deleted `Library`'s finalizer
when its cleanup `Job` succeeds, and the `Job` deletes its own run
before it exits. Built on 2026-10-07, and proved on `liken-1` the same
day.

## The problem

The cleanup `Job` of a deleted `Library` deletes every row of the
library, then writes its own `runs` row and waits for a catalog pod's
confirmer to write a `confirmations` row for it. Nothing deleted those
two rows. They kept the library's key in the catalog, so the
namespace reporter published a report for the library each time the
catalog changed, and the operator's next pass cleared that report
again. On `liken-1` on 2026-09-27, the `Library` `drill-action`,
deleted on 2026-09-02, still held one `runs` row: its cleanup `Job`'s
own run.

A second path left a `confirmations` row behind. A confirmer can
confirm a run in the moment after a cleanup `Job` deletes the run's
row and before the confirmer's run stream carries that delete.

The operator released the finalizer when the `Job` succeeded and the
reporter's report showed the `Job`'s run. That report existed only
because the row existed, so the `Job` could not delete the row. The
operator cannot delete it either, because the Corrosion API binds
loopback inside each pod.

## The design

**The release rule.** The operator releases the finalizer when the
cleanup `Job` succeeds. The `Job` exits zero only after a standing
catalog pod confirmed its run, so the reporter's echo repeated a proof
the confirmation already gave. The `Departing` condition loses its
`AwaitingEcho` reason.

**The `Job`'s last step.** After the confirmation, the `Job` deletes
its own `runs` row. The delete needs the same proof as the sweep,
because an agent that receives `SIGTERM` drops the broadcasts it has
not sent. A confirmation cannot answer a delete, because the run it
would name is the row the delete removes. So the confirmer answers
the delete by deleting the confirmation.

**The confirmer's drop.** The confirmer deletes every confirmation
whose run is gone from the `runs` table, whichever pod wrote it. It
does this on each delete that its run stream carries, and once each
time a stream opens, because a delete made while no stream was open
reaches the confirmer as a missing row in the snapshot, with no delete
event. The `Job` never deletes a confirmation itself, so a
confirmation that leaves the `Job`'s copy is one that a standing pod
dropped after it held the delete. The `Job` exits zero on that drop,
or on a stream that opens with no confirmation of its run, and fails
after `HANDOFF_TIMEOUT` otherwise. The drop also answers the second
path, because the late confirmation's run is gone when the delete
arrives.

The `Job` writes nothing while it waits for the drop. A hand-off writes
its run again every ten seconds to make a fresh broadcast, but a delete
has nothing to write again. The standing pod already holds this
agent's versions up to the run, so its periodic sync pulls the one
version the delete makes. The drill measures how long that takes.

## Considered and set aside

- **The standing confirmer deletes a confirmed cleanup run after the
  hand-off timeout.** A standing agent's delete needs no proof, but it
  adds a clock to the confirmer, and the `Job` must see its
  confirmation before the delete.
- **The reporter publishes no report for a key that holds only a
  cleanup run and its confirmations.** The rows stay in the catalog
  for good, and the finalizer release must stop reading the report
  anyway.
- **The `Job` deletes its run and every confirmation of it.** The
  confirmer's drop would then delete nothing in a copy that received
  both deletes together, and the `Job` would have no answer to wait on.

## What is left by hand

A row from before this change stays, because no confirmation answers
it. On `liken-1`, the `drill-action` cleanup run and the `cleanup` run
that the live `Library` `franchises` carried from a deletion under the
same name on 2026-09-06 were deleted by hand on 2026-10-07.

## The proof

The tests run the cleanup `Job` against a real confirmer on the
in-memory catalog, and check that the departed library holds no row in
any table of the catalog afterward. Other tests cover the confirmer's
drop on a delete, on a late confirmation, and on a stream that opens,
and the `Job`'s failure when no pod drops the confirmation.

The drill on `liken-1` on 2026-10-07, with one catalog pod: a
`Library` `drill-cleanup` of kind `franchises` walked the 36
franchises of the lab's repository. It was then deleted. The cleanup
`Job` swept 1,573 rows, the confirmer confirmed its run and then
dropped that confirmation, and the `Job` logged that a catalog pod held
its delete. The operator released the finalizer 22 seconds after the
delete. Afterward, none of the 23 tables in `catalogTables` held a row
for `default/drill-cleanup`. When the new confirmer started, it also
dropped 3 confirmations whose `scan` runs a later walk `Job` had
replaced.
