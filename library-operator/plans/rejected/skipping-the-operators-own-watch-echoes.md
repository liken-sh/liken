# Skipping the operator's own watch echoes

Built on branch `library-echo-wakes` (commit `6d631df`) on 2026-09-27,
measured on `liken-1`, and set aside. The wakes it removes are a small
part of the passes the operator runs, and the filter adds state with a
time window to the path that wakes the loop.

## What the branch did

The operator watches the collections it also writes: the status of a
`Library` and a `Catalog`, and the finalizers on a `Library`, a `Play`,
and a `Person`. Each write comes back on the watch as an event, and
that event wakes a pass that finds nothing to do. The branch made the
API client record the `resourceVersion` that each `PUT` and `PATCH`
answered with, and made each watch skip the one event that carries a
recorded version. A version whose event does not arrive within one
minute is dropped from the map.

The branch also stopped one repeated wake. A deleted `Library` whose
catalog rows remain is reported again by the namespace reporter each
time the catalog changes. Each report woke a pass, and that pass only
cleared the report again. The branch made the report desk remember the
keys the last pass dropped, and a report for one of those keys woke no
pass. [A cleanup run outlives its
`Library`](../open-problems/a-cleanup-run-outlives-its-library.md)
describes why those rows remain.

## What the testbed measured

- At rest, the pass ran only on the 10 s backstop: 6 passes a minute.
  No echo and no repeated report woke the loop at rest.
- During a walk of the `franchises` library, the operator ran 17
  passes in 40 s. The backstop caused 4 of them. The echoes of the
  operator's own writes caused a few of the other 13. Other changes on
  the watches and the bus caused the rest.

The filter saves a few passes during a scan, and each pass that finds
nothing changed sends only reads. The cost is a version map shared by
the client and every watch, a one-minute window, and a rule about
which writes the map records. A mistake in that rule loses a wake that
the pass needs, and the backstop then delays the reaction by up to
10 s.

## What would make it worth taking up

- A measurement where the echo passes are a large share of the
  passes, for example many `Library` objects in one cluster that
  each write status during a scan.
- A pass that costs much more than it does now, so that one extra
  pass is a real load on the API server.
- A removal of the backstop. Without the 10 s pass, the passes that
  echoes cause would be a larger share of the total.

The repeated report of a deleted `Library` has a better answer than a
filter: remove the rows that cause the reports.
