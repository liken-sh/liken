# Every node holds every table

A Corrosion agent replicates the whole cluster's database. There is
no per-table or per-row filter: a screen that shows only the films
library still holds the series catalog, and the day a photo library
of a hundred thousand rows joins, every screen holds it too. The
proof of concept measured 105,000 rows at 132 MB on disk on the
writer's node and about 210 MB on the others, and 74 MB of resident
memory at rest, which a one-gigabyte box can carry for one such
kind and not for several.

Three shapes could answer it, and none is chosen. One cluster per
family of kinds, so screens join the clusters they need and a photo
cluster stays off a films-only kiosk; that costs one sidecar per
cluster per pod. A smaller row for the large kinds, with the body
left on the share and only the header in the catalog; that keeps one
cluster and moves the size to the share. Or a filter in Corrosion
itself, which the project would carry as a patch.

The decision waits for the first large kind, because the numbers
that decide it are that kind's row size and count. Plan 12 names it.
