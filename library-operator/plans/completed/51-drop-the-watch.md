# Drop the Watch

Plan 51. The `Watch` kind is gone: the CRD, its status projection,
the `watch` field on the play request, and the bus topic that carried
the projection. Completed 2026-09-08.

## The problem

Plan 14 defined a `Watch` as one set of people on one item, and had
the browser create one whenever it asked who was watching. Nothing
ever created one. The house cluster held zero, and the browser's
`watch` message stayed on plan 41's owed list. Meanwhile the record
it was meant to hold already exists without it: a `Play` names its
people through owner references, the store's `play_people` rows carry
the same set, and the browser's continue row reads the plays of the
people at the screen. A group that sits down together names itself
every time it plays, so a resource for the group records nothing new.

A kind that nothing creates is still a promise in the CRD reference,
a rule in the RBAC, a watcher in the operator, a projection the
progress role publishes after every write, and a page on the site. So
it goes.

## What changed

- The `Watch` types, the `watches` CRD, its RBAC rules, its watcher,
  and the API client's reads and status writes.
- The `watch` field on the play request and on the audience message,
  and the `Watch` owner reference on a `Play`.
- The `watches/{namespace}/{watch}/progress` topic and the projection
  the progress role published on it after every row it wrote.
- The site's `Watch` reference page and every row and section of the
  bus page that named it.

## What stays

The store's `plays.watch` column and its `plays_watch` index. Corrosion
refuses to drop a column or an index, so both stay in the schema with
a comment that says nothing writes or reads them. Every row holds the
column's default, the empty string.

## What comes next

"Next up" across series, sets, and franchises is the ambient system
this makes room for. It reads the plays of the exact set of people at
the screen, and it needs no resource beyond the `Play`.
