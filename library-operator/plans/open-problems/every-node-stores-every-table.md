# Every node stores every table

A Corrosion agent replicates the whole cluster's database. There is no
per-table or per-row filter. A screen that shows only the films library
also stores the series catalog, and when a photo library of a hundred
thousand rows joins, every screen stores it too. The proof of concept
measured 105,000 rows at 132 MB on disk on the writer's node and about
210 MB on the others, and 74 MB of resident memory at rest. A
one-gigabyte box has room for that for one such kind.

Three shapes could answer it, and none is chosen. One cluster per family
of kinds, so a screen joins the clusters it needs and a photo cluster
stays off a films-only kiosk, at the cost of one sidecar per cluster per
pod. A smaller row for the large kinds, with the body left on the volume
and only the header in the catalog, which keeps one cluster and moves
the size to the volume. Or a filter in Corrosion itself, which the
project would keep as a patch.

The decision waits for the first large kind, because that kind's row
size and count decide it. Plan 13 names it.