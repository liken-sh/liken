# 66, One entry for each person

Plan 66. Each person has one `.contributors/` entry in a library,
whichever provider named them. When two entries hold the same id in one
scheme, the enricher merges them into one entry. The walk finds the
entries that must merge, and the enricher merges them in the same run.
A fact that adds an id to an entry can open a merge too.
A credit also finds its entry by id before it looks by name, so fewer
duplicates occur.

Built on 2026-09-25 in 7ac9ae3, and run on `liken-1` on 2026-09-26
with library-operator 2026.09.19-002-dev-020-ae23b024 and later. The
build stored the ids in a new table and not in a new key; "What the
build changed" at the end records that and the other changes. A merge
of real duplicates is not drilled yet.

The need came from the 2026-09-25 review of [plan
33](33-the-imdb-datasets.md). Plan 33 adds IMDb's datasets as a
fallback source of credits, and a credit from the datasets names a
person by the IMDb id alone.

## The problem

The credits fact finds a person's entry by the slug of the name
(`contributorFor`, `contributors.go:160`). It reads the entry at the
plain slug, then the entry at the slug with the id suffix, such as
`tom-hanks-tmdb-31`. `isPerson` (`contributors.go:142`) decides whether
an entry is the credited person:

- When the entry and the credit hold an id in the same scheme, the ids
  decide.
- When they share no scheme, the entry counts as the same person.

This join works when TMDb names every person, because every credit
then has a TMDb id. It fails in two ways when a credit has a
different scheme:

- **A duplicate.** IMDb and TMDb spell one name differently, so the
  slugs differ. The person gets two entries: one with the TMDb id, and
  one with the IMDb id.
- **A wrong join.** Two people have the same name. The entry at the
  plain slug holds only a TMDb id, and a credit holds only an IMDb id.
  They share no scheme, so the credit joins the wrong person.

The `contributor.ids` fact later adds the other ids from TMDb's person
record. After that, two entries can hold the same IMDb id. The catalog
cannot show this, because `contributor_aliases` keys on
`(library, scheme, id)` and keeps one path for each id
(`corrosion/schema/catalog.sql:440`).

## The design

### A credit finds its entry by id first

Before the credits fact reads a slug, it looks up each id of the credit
in `contributor_aliases` in the local catalog. When one id resolves to
an entry, the credit links to that entry, whatever its slug is. The
slug join runs only when no id resolves.

A credit that has an IMDb id and no TMDb id asks TMDb's find endpoint
for the TMDb id first, when the `Library`'s sources name a `Ready`
`tmdb` provider. So the slug join then compares TMDb ids, and a person
of the same name does not join the wrong entry. The call costs one
TMDb request for each new person. A person who already has an entry
resolves by id and costs no request.

### The contributor.ids fact fills an entry that has no TMDb id

`contributor.ids` keys on the TMDb id today, so it skips an entry that
the IMDb datasets created. For such an entry, the fact asks TMDb's find
endpoint for the TMDb id by the IMDb id. It writes the TMDb id into
`contributor.yaml` and then fills the entry as it does for every other
entry. The biography and headshot facts then fill that entry too.

### The walk records every id of every entry

`contributor_aliases` keys on `(library, scheme, id, path)`. The walk
writes a row for each id of each entry, so two entries that hold one id
are two rows. The `contributor.merge` gap is the ids that have more
than one path in one library.

Corrosion refuses to change the primary key of a table in a database
that it already holds. Plan 57 deletes three of each `Library`'s catalog
claims, and this key change lands in the same release, so every copy
starts clean once.

### The merge

A merge changes two kinds of file, and each kind keeps the one writer
it has today:

- `contributor.ids` writes `contributor.yaml` after the credits fact
  creates it, and its fight check compares the file with the hash in its
  ledger. So `contributor.ids` merges the entries, in the contributors
  container.
- The credits fact is the only writer of each title's `credits.yaml`.
  So the credits fact moves the credits, in the nfo container.

The entry that a merge removes keeps a `contributor.yaml` with one
field, `mergedInto`, which is the path of the entry that stays. This
file is the record that the credits fact reads to move the credits. It
stays only until the credits move.

For each group of entries that share an id, `contributor.ids` does
these steps:

1. **It chooses the entry that stays.** That is the entry at the plain
   slug. If no entry of the group is at the plain slug, the entry with
   the most ids stays.
2. **It joins the ids.** The entry that stays gets every id of the
   others. If two entries hold different ids in one scheme, they are two
   people and one of them has a wrong id. The merge stops for that group
   and writes a `conflict` attempt that names both paths.
3. **It moves the files.** `biography.txt` and `headshot.jpg` move to
   the entry that stays when that entry has no file of that name.
4. **It writes `mergedInto`** into each removed entry.

The credits fact then has a gap: each credit row whose `contributor`
names an entry with `mergedInto`. The `credits` table's index on
`(library, contributor)` finds those titles. The fact rewrites the
`contributor` path in each title's `credits.yaml`.

`contributor.ids` deletes a removed entry when no credit row names it.
The walk reads an entry with `mergedInto` as a record of a merge and
not as a person, so the person pool and the person pages never show it.

Both gaps loop as every phase does in plan 57. So a merge that
`contributor.ids` opens during a run finishes in the same run.

A `contributor.yaml` that a person edited by hand does not merge.
`contributorHeldByAnother` (`contributorfacts.go:192`) already detects
a hand edit. The merge writes a `held` attempt for that group, and the
report counts it.

### When a merge runs

- **During a walk.** The walk rewrites the alias rows from the entries
  on disk, so the gap shows every duplicate that exists, including the
  duplicates in a library today.
- **When a fact adds an id.** `contributor.ids` writes the new ids and
  their alias rows at once (plan 34). The credits fact's next pass finds
  the gap.

## What was set aside

- **A permanent redirect in the removed entry.** `mergedInto` could
  stay, and no `credits.yaml` would change. But then every reader of
  the store must follow redirects, and the removed directories stay
  for good. In this plan the field exists only until the credits move.
- **One writer for the whole merge.** The credits fact or
  `contributor.ids` could do all of it. Then one of them writes a file
  that the other owns, and the fight check reads the merge as a hand
  edit.
- **A merge in the walk.** The walk mounts the library volume
  read-only, and it writes no file.
- **A merge by name alone.** Two people can have the same name. Only
  a shared id decides that two entries are one person.

## What the build changed

- **The ids are in a new table, `contributor_ids`,** keyed by
  `(library, path, scheme)`, with an index on `(library, scheme, id)`.
  `contributor_aliases` keeps its key. Corrosion adds a new table to a
  database it already holds, and it refuses a new primary key, so no
  catalog copy had to start again.
- **A second new table, `contributor_merges`,** holds each removed
  entry and the entry that stays. Without it, the move of the credits
  and the delete of a removed entry have no catalog row to read.
- **`contributor.merge` and `credits.move` are gaps, not facts.** They
  are not in the fact vocabulary, so `spec.refresh` and the CLI's fact
  list do not change. `gapFact` names the fact whose container runs
  each gap, and `phaseGapNames` makes a phase and the operator's check
  read those gaps as well as the gaps of their own facts.
- **A merge writes its attempts into its own ledger,**
  `.liken/contributor.merge.yaml`, so they do not replace the attempts
  of `contributor.ids`.
- **The move of the credits waits for the next `Job`.** The nfo phase
  moves the credits, and it can end before the contributors phase
  merges. The contributors phase of that next `Job` deletes each
  removed entry that no credit names.
- **When two entries are both at a plain slug,** because a provider
  spelled the name another way, the entry with more ids stays.

## The drill on `liken-1`

A full walk of the movies and the series filled `contributor_ids` from
the entries on disk. The `contributor.merge` gap then read 0 in both
Libraries, so the testbed has no two entries that hold one id. No
merge ran.

## What is still open

- **A merge of real duplicates is not drilled.** The testbed's
  libraries are on a volume that other people use, so the drill needs
  a copy of a library with two entries for one person: one with the
  TMDb id, and one with the IMDb id under another spelling.
- **An existing catalog has an empty `contributor_ids` until the next
  full walk.** Until then a credit falls back to the join by slug.
- **The check before a delete counts catalog rows.** A `credits.yaml`
  that no walk has read yet does not count.
