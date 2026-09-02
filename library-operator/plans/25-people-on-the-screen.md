# People on the screen

Plan 25. A stub for a later agent to shape. It makes the credited people
of a library, the cast and the crew, something a person can follow from
one title to the next.

## Blocked on plan 30

This plan waits for [plan 30](30-facts-art-and-contributors.md). A
sidecar names a person by name alone: Jellyfin's NFO writer puts a
name, a role, a type, a sort order, and a thumb path inside `<actor>`,
and no provider id, and the thumb path points into Jellyfin's own
metadata directory and not the volume. Plan 30 writes each person to
`.contributors/` with ids under every scheme and a headshot, links
each credit to that entry in `credits.yaml`, and derives the
`contributors` and `credits` tables. This plan draws those tables.

## The problem

Every movie and every episode carries its cast with roles, its
directors, and its writers in the body the scanner reads from the
sidecar. Plan 22 draws them as text under the credits. A name is a
dead end: select does nothing on it, and nothing on the screen answers
"what else is this person in." Plan 14 is about a different kind of
person, the one holding the remote, and this plan does not touch that.

## The shape

- The cast and credits rows become focusable, one press below the set
  strip, and select on a name opens a person's page: the name, a
  headshot where the volume holds one, and every title in the
  namespace's libraries the person is credited in, as a wall with the
  role under each slot.
- The page reads the `contributors` and `credits` tables plan 30
  derives from `.contributors/` and `credits.yaml`, and joins one
  person across the namespace's libraries by any shared id.
- Headshots come from `headshot.jpg` in the person's entry.

## What is not decided

Whether a person's page shows episodes one by one or the series they
appear in. Whether the people table is worth its size on a one-gigabyte
screen for a library with tens of thousands of credits.
