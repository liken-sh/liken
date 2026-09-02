# People on the screen

Plan 25. A stub for a later agent to shape. It makes the credited people
of a library, the cast and the crew, something a person can follow from
one title to the next.

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
- The scanner derives a people table from the bodies it already reads,
  the way plan 22 derives sets from the movies that name them, and a
  credits table that joins a person to a title and a role. A person is
  identified by name until enrichment gives a provider id, and the row
  says which.
- Headshots come from Kodi's `.actors` folder beside a title where a
  tool wrote one, and from the enricher of plan 11 where none did.

## What is not decided

How two people with one name are told apart before enrichment, and
whether a wrong merge is worse than a split. Whether a person's page
shows episodes one by one or the series they appear in. Whether the
people table is worth its size on a one-gigabyte screen for a library
with tens of thousands of credits.
