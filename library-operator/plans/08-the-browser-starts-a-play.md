# The browser starts a Play

Plan 08. From a chosen title to a `Play` on the same `Player`, and
back to the browser when it ends. At the end of this plan the design's
first outcome is complete: index, browse, pick, play.

## The problem

The browser holds the title and the `Player` it is drawing on. A
`Play` needs a URI the `Player` can open, the thumbnail sidecars the
display draws on the scrub bar, and a start position. And someone has
to create it. The idle pod holds no API credential today, by
`media-operator`'s rule, and the browser should not be the first pod
to carry one.

## The contract

**The ask goes over the bus.** The browser publishes a request on a
topic under this operator's base: the `Player` it is on, the catalog
item it chose, and nothing else. The library operator, which holds
the RBAC, reads the request, resolves the item to a `Play`, and
creates it in the `Player`'s namespace. The screen stays
credential-free, the bus is already the screen's one path out, and a
swapped-in browser that speaks the same topic gets the same service.

**Resolving an item.** The operator builds the `Play` from the
catalog and the `Library`: the item's path becomes an `nfs://` URI
from the storage source plan 02 resolved, checked against the
schemes the `Player` accepts; the `.trickplay` path becomes the
trickplay reference the display already reads; a series item becomes
a list, the chosen episode first and the rest of the season after it,
so "play the season" is one `Play`. The start position is the
beginning in this plan, because there is no watch state yet.

**The change below: the current item.** `Play.status` reports the
position inside the current item and not which item. For a list of
episodes, resume needs the index. This plan adds that field to the
`Play` status in `media-operator`, and the sidecar that reports
position reports the item with it. It is a media fact and it is cheap;
nothing in this plan reads it yet, but the watch-state plan cannot
exist without it.

**Handing the screen over.** While the `Play` runs, its pod draws over
the idle surface, as today. The browser watches the `Play` through the
`Player`'s status topic, keeps its place, and when the `Play` ends it
is where the person left it, on the same wall with the same focus, not
at rest.

## The local harness

`local/play` runs the browser against the workstation catalog and,
on select, prints the `Play` it would ask for instead of publishing
it, so the URI, the trickplay reference, and the list order can be
checked with no cluster.

## What was set aside

A credential on the browser pod. It would make the browser the first
screen-side process that can write to the API server, and the bus
already carries every other request a screen makes.

Resume. It needs the watch-state plan, and this plan makes sure the
`Play` reports enough for it.

## Proof

On `liken-1`: from the wall, select on a film creates a `Play` on that
`Player` within a second, the film starts, the scrub bar shows the
thumbnails, and `kubectl get plays` shows it running. Back or the end
of the film returns the screen to the wall at the same focus. Select
on an episode plays it and the rest of its season in order, and the
`Play` status names the current item as it advances.
