# Which libraries a screen shows

The media browser in plan 07 shows every library in its namespace. A
children's room should show the children's movie library and nothing
else, and a living room should open on "continue watching" and "recently
added" rather than a list of libraries. Both are facts of this layer. By
the one-way rule they cannot be fields of the `Player`.

The shape is a resource in this operator that names a `Player` and the
libraries it may browse, all in its own namespace, with both relations
many-to-many. It may also
name the rows the first view shows, in order: continue watching,
recently added from a library, a hand-made collection, or a grouping
such as a set or a decade. Rows are queries against the catalog, so a
row costs no new data. A hand-made collection is a folder of symlinks on
the volume that the scanner reads as an attribute. A ratings ceiling,
from the sidecars' certification fields, keeps a title above the ceiling
off a children's screen.

Nothing about it is settled: the resource's name, whether rows are
configuration at all or code with one field for the ceiling, and whether
"continue watching" belongs here or in plan 14. It waits for the first
screen that needs it.
