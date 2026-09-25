# Which libraries a screen shows

The media browser in plan 07 shows every library in its namespace. A
screen in a children's room should show the children's movie library
and nothing else. A screen in a living room should open on "continue
watching" and "recently added" instead of a list of libraries. Both
settings belong to this layer. `media-operator` does not depend on this
operator, so these settings cannot be fields of the `Player`.

The proposed design is a resource in this operator that names a
`Player` and the libraries that the `Player` may browse, all in the
resource's own namespace. Both relations are many-to-many. It may also
name the rows the first view shows, in order: continue watching,
recently added from a library, a hand-made collection, or a grouping
such as a set or a decade. Rows are queries against the catalog, so a
row needs no new data. A hand-made collection is a folder of symlinks on
the volume that the scanner reads as an attribute. A ratings ceiling,
from the sidecars' certification fields, hides a title above the
ceiling on a children's screen.

Nothing about it is decided: the resource's name, whether rows are
configuration at all or code with one field for the ceiling, and whether
"continue watching" belongs here or in plan 14. The work starts when
the first screen needs it.

[Plan 26](../completed/26-the-home-page.md) builds the first view as
strips, each one a `Query` from a fixed set of kinds, and draws the same
strips on every screen. Plan 26 decides none of the questions above.
When the resource exists, it names a screen's libraries and its rows
with those kinds of `Query`.
