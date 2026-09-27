# A cleanup run outlives its `Library`

The cleanup `Job` of a deleted `Library` deletes every row of the
library, then writes its own `runs` row as its last write. Each catalog
pod's confirmer then writes a `confirmations` row for that run. Nothing
deletes these rows. They keep the library's key in the catalog after
the `Library` is gone, so the namespace reporter publishes a report for
the library each time the catalog changes, and the operator's next pass
clears that report again.

## The evidence

On `liken-1` on 2026-09-27, the `Library` `drill-action` in namespace
`default` was deleted on 2026-09-02. The catalog holds no row for it in
any table except one `runs` row:

| worker | job | started, finished | actor | version |
|---|---|---|---|---|
| `cleanup` | `drill-action-cleanup` | 2026-09-02 18:33:36 UTC | empty | 0 |

That is the cleanup `Job`'s own run. The sweep worked, and the row it
wrote last is the one row it left. The `runs` table also holds a
`cleanup` row from 2026-09-06 for the live `Library` `franchises`, which
was deleted and declared again under the same name. That row is part
of the new library's report.

The empty actor and the zero version show that this `Job` ran before
cleanup `Job`s waited for a confirmation, so no confirmer wrote a
`confirmations` row for it. The current code leaves the same `runs`
row, with an actor and a version in it, and one `confirmations` row
for each catalog pod that confirmed the run:

- `cleanup.go` sweeps the rows, then calls `handOff`, which writes the
  `runs` row and waits for a `confirmations` row. The comment on
  `sweep` says that the only rows the library holds after the sweep are
  that run and the confirmations that answer it.
- `confirmer.go` writes one `confirmations` row per catalog pod for
  the run, and prunes only the confirmations of other `Job`s of the
  same worker. It keeps the row of the newest run.
- `cleanupjob.go` releases the finalizer when the `Job` succeeded and
  the reporter's report of the library shows that `Job`'s run
  (`cleanupComplete` and `cleanupEchoed`). The report exists only
  because the row exists.
- `reporter.go` publishes a retained report for every key that
  `LibraryKeys` finds, and `LibraryKeys` reads the `library` column of
  every table in `catalogTables`, `runs` and `confirmations` included.
  It publishes every key it knows again on each change that an update
  stream carries.
- `operate.go` clears the report topics of every key that no `Library`
  holds, on each pass. The next catalog change publishes the report
  again, and the report wakes one more pass.

## Why this is not a one-line fix

The `runs` row is the proof that the finalizer release reads, so the
`Job` cannot delete it before it exits. The operator cannot delete the
rows after
the release, because the Corrosion API binds loopback inside each pod,
and the operator has no pod in the catalog cluster. Each option below
changes who holds the proof, or adds a clock:

- Release the finalizer on the `Job`'s success alone. A cleanup `Job`
  exits zero only after a standing catalog pod confirms its run, so the
  echo check repeats what the confirmation already proves. The `Job`
  can then delete its `runs` row and its `confirmations` row after the
  confirmation arrives. That delete also needs a confirmation before
  the agent exits, because an agent that receives `SIGTERM` drops the
  broadcasts it has not sent, and the `runs` row that a confirmation
  answers is the row the delete removes.
- Make the confirmer, which runs in the standing catalog pod, delete a
  confirmed `cleanup` run and its `confirmations` rows after the
  hand-off timeout. A standing agent's delete does not need a
  confirmation. The cost is a clock in the confirmer, and the `Job`
  must see its confirmation before the delete.
- Make the reporter publish no report for a key whose only rows are a
  `cleanup` run and the `confirmations` rows that answer it. The rows
  then stay in the catalog, and the finalizer release must stop
  reading the echo.

A new plan should choose one of these. The first option also removes
the stale `cleanup` row that a `Library` declared again under an old
name carries in its report.

The `drill-action` row on `liken-1` needs a manual delete in every
case, because none of these options removes a row from before the
confirmation.
