# 20, No edge kind filter

Proposed and built on 2026-11-21.

## The problem

Every page that draws edges had a row of checkboxes, one for each kind
of relation, such as "runs on", "stores on", and "allowed to call". A
checkbox turned its kind off for the view. The `edges` query parameter
listed the kinds that were on, and the server added that parameter to
every other link on the page, so the next page showed the same kinds.

The maintainer never used the row. It also changed nothing in the
drawing of a page with a sketch, because a sketch draws every role
whatever the row selected, as plan 13 records. The filter still cost
code on every page: each page builder took the kinds as an argument,
the graph filtered each query by them, the model cached its loops and
forges once for each set of kinds, and each rendered page went through
a rewrite of its links.

## What was removed

- The row: `map/edge_row.html`, `EdgeRow`, `Toggle`, `ON_THE_MAP`, and
  `with_edges` in `transect-map`, the `edge_row` field and the
  `with_edge_row` builder of each page, and the `.edge-menu` and
  `.edge-kind` styles in `layout.css`. The section page's legend no
  longer shares a `.section-key` row with it.
- The parameter: `Shown` and `TOGGLED` in `transect-graph`,
  `Kind::slug`, the `edges` field of `view::Query`, and `carry::carry`,
  which wrote the parameter onto links. No code reads `edges`. An old
  URL that has `?edges=` loads the page with every kind, because the
  query extractor ignores a parameter that `Query` does not name.
- `View`, which paired a `Page` with its kinds. A `Page` now keys each
  render and each event stream.
- The filtering: `Graph::only` and the kinds that a `Graph` carried,
  `Snapshot::offered`, the empty `Allowed` that `Model::allowed`
  returned when allowed-to-call was off, the `stores_on` argument of
  `Ground::build`, and the `shown` argument of every page builder.
- The cache for each set of kinds: `EdgeSet` and its memo. The model
  computes `loops`, `forges`, and `recorded` once when it is built.
- `Kind::meaning`, which only the row read.

## What stays

Every kind of relation is shown and counted on every page: a selected
namespace's relations on the cluster map, the loop marks, the cascade
of a lost machine, the ground's volume counts, a pod's devices, and
the side boxes and lines of a section.

The kind list that the row read is still the list that decides when a
clue draws no guessed depends-on edge, so it moved to `RELATED_BY` in
`transect-network`'s `addresses::guess`. `carry_loss` and
`carry_access` still rewrite links, for the loss view and the access
lens.

The row's checkboxes were the only place where a page stated what each
kind means. No page states it now.
