# Shelves and rooms

Plan 13. A stub, at low fidelity, for a later agent to shape. It
answers two questions the first browser does not: which libraries a
screen shows, and what its first screen holds.

## The problem

The browser in plan 07 shows every library it can see. A children's
room should see the children's film library and nothing else, and a
living room should open on "continue watching" and "recently added"
rather than a list of libraries. Those are facts of this layer, and
by the one-way rule they cannot live on the `Player`.

## The shape

- A resource in this operator, working name `Shelf`, that names a
  `Player` and the libraries it may browse, and the rows its first
  screen shows, in order: continue watching, recently added from a
  library, a hand-made collection, a grouping such as a set or a
  decade. Both relations are many-to-many.
- Rows are queries against the catalog, so a row costs no new data.
  Hand-made collections are a folder of symlinks on the share that the
  scanner reads as an attribute, which keeps the filesystem the place
  a person expresses intent and the catalog the place queries run.
- Ratings ceilings, so a children's screen never lists a title above
  its rating, taken from the sidecars' certification fields.

## What is not decided

The resource's name. Whether rows are configuration at all or code
with one `Player` field for the ceiling until a second need shows up.
Whether "continue watching" belongs here or in the watch-state plan.
