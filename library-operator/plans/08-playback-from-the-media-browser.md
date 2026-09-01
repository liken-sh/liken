# Playback from the media browser

Plan 08. From a chosen title to a `Play` on the same `Player`, and back
to the media browser when it ends. At the end of this plan the first
outcome is complete: index, browse, pick, play.

## The problem

The media browser has the title and the `Player` it draws on. A `Play`
needs a media reference the `Player` accepts, the trickplay sidecar
the display draws on the scrub bar, and the words the film's own
display shows. And something has to create it. The screen pod holds no
API credential, by `media-operator`'s rule, and the media browser is
not the first pod to get one.

What the lower layer already gives this plan, read from
`media-operator` as it stands:

* A `Play` names its `Player`s and a list of items, each an item with
  a `uri` and a `presentation`: the title, the series, the season and
  episode, the year, the art, and the trickplay reference.
* The `claim://<claim>/<path>` scheme, plan 19 there, mounts a claim
  in the `Play`'s namespace read-only on the playback pod. The
  `Library` and the `Player` share a namespace, so a `Play` names the
  library's own claim and no second claim is created.
* `Play.status.item` already reports which item of the list plays,
  beside the position inside it. An earlier draft of this plan asked
  for that field; it exists, and the watch-state plan reads it later.
* When the `Play` ends, the idle command pod states `present` on the
  screen topic, and the browser maps a fresh surface at the focus it
  held, which plan 07 built.

## The contract

**The request goes over the bus.** On select, on a movie or an
episode, the browser publishes a request on a topic this operator
names. The operator sets the topic on the browser container, beside
the three variables plan 07 set, because the browser knows neither
this operator's topic base nor the `Player`'s name. The request
carries the library key, the kind, and the item id, the same three
values the browser's own rows carry. The operator, which has the
RBAC, reads the request, resolves the item, and creates the `Play` in
the `Player`'s namespace.

The screen holds no credential, the bus is already the screen's one
path out, and a media browser of another make that speaks the same
topic gets the same service.

**Resolving an item.** The operator builds the `Play` from the catalog
and the `Library`. A title's main file, from `files` through
`file_items`, joined to the `Library`'s claim and root, becomes
`claim://<claim>/<root>/<path>`. The file's `trickplay` column becomes
the trickplay reference in the same form. The row's title, year, and
series fields fill the presentation, so the film's display shows the
words the wall showed. A movie is one item. An episode becomes a list,
the chosen episode first and the rest of its season after it in
episode order, so one `Play` plays the season. The start is the
beginning, because there is no watch state yet.

A request that names an item the catalog no longer holds, or a
library with no claim, is logged and dropped. The browser draws what
the catalog holds, so the case is a race with a prune and not a
person's mistake.

**Who may ask.** The operator subscribes to the request topics of the
`Player`s it serves, and it creates a `Play` only on a `Player` whose
`status.idle.controller` is this operator's name. A request for any
other `Player` is dropped.

**The screen during playback.** While the `Play` runs, its pod draws
over the browser's surface and the unit's presses go to the film's
translator, so the browser receives nothing and changes nothing. When
the `Play` ends, `present` returns the browser at the same focus.

## The local harness

`local/browse` publishes the request when the topic variable is set
and does nothing on select when it is not, so the workstation needs no
cluster and no broker to browse. The resolve runs against a fixture
catalog in the Go tests, where the reference, the trickplay path, and
the list order are checked.

## What was set aside

A credential on the media browser pod. It would make the browser the
first screen-side process that writes to the API server, and every
other request a screen makes already goes over the bus.

Resume. It needs the watch-state plan. `Play.status.item` and the
position are already enough for it.

A `local/play` script that prints the `Play` instead of publishing it.
The Go tests check the same thing against a fixture catalog, and a
second harness would drift from the first.

## Proof

On `liken-1`: from the wall, select on a movie creates a `Play` on that
`Player` within a second, the movie starts, the scrub bar shows the
thumbnails, and `kubectl get plays` shows it running. Back, or the end
of the movie, returns the screen to the wall at the same focus. Select
on an episode plays it and the rest of its season in order, and the
`Play` status names the current item as it advances.
