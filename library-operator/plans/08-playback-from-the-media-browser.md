# Playback from the media browser

Plan 08. From a chosen title to a `Play` on the same `Player`, and back
to the media browser when it ends. At the end of this plan the first
outcome is complete: index, browse, pick, play.

## The problem

The media browser has the title and the `Player` it draws on. A `Play`
needs a media reference the `Player` accepts, the thumbnail sidecars the
display draws on the scrub bar, and a start position. And something has
to create it. The idle pod holds no API credential, by
`media-operator`'s rule, and the media browser is not the first pod to
get one.

## The contract

**The request goes over the bus.** The media browser publishes a request
on a topic under this operator's base: the `Player` it is on and the
catalog item it chose. The operator, which has the RBAC, reads the
request, resolves the item, and creates the `Play` in the `Player`'s
namespace. The screen holds no credential, the bus is already the
screen's one path out, and a media browser of another make that speaks
the same topic gets the same service.

**Resolving an item.** The operator builds the `Play` from the catalog
and the `Library`. The item's path becomes a media reference from the
volume behind the claim, checked against the schemes the `Player`
accepts. The `.trickplay` path becomes the trickplay reference the
display reads. A series item becomes a list, the chosen episode first
and the rest of the season after it, so one `Play` plays the season. The
start position is the beginning, because there is no watch state yet.

**The change below: a reference that names a claim.** `media-operator`
resolves `https://` and `nfs://`, so an NFS-backed volume yields an
`nfs://` reference and needs no change. A Longhorn volume or a local
disk has no address a URI can name. For those, this plan adds to
`media-operator` a media reference that names a claim in the `Play`'s
namespace and a path inside it, resolved to a volume mount on the
playback pod. The library operator then creates a claim for the
library's volume in the `Player`'s namespace, or documents that the
person does. Which of the two is decided when the plan is built.

**The change below: the current item.** `Play.status` reports the
position inside the current item and not which item. Resume of a list
needs the index. This plan adds that field to the `Play` status in
`media-operator`, and the sidecar that reports position reports the item
with it. Nothing in this plan reads it yet. The watch-state plan cannot
exist without it.

**The screen during playback.** While the `Play` runs, its pod draws
over the idle surface, as today. The media browser reads the `Play`'s
progress from the `Player`'s status topic and keeps its place. When the
`Play` ends, the media browser shows the same wall with the same focus.

## The local harness

`local/play` runs the media browser against the workstation catalog. On
select, it prints the `Play` it would request instead of publishing it,
so the reference, the trickplay path, and the list order can be checked
with no cluster.

## What was set aside

A credential on the media browser pod. It would make the media browser
the first screen-side process that writes to the API server, and every
other request a screen makes already goes over the bus.

Resume. It needs the watch-state plan. This plan makes sure the `Play`
reports enough for it.

## Proof

On `liken-1`: from the wall, select on a film creates a `Play` on that
`Player` within a second, the film starts, the scrub bar shows the
thumbnails, and `kubectl get plays` shows it running. Back, or the end
of the film, returns the screen to the wall at the same focus. Select on
an episode plays it and the rest of its season in order, and the `Play`
status names the current item as it advances.