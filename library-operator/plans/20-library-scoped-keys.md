# Library-scoped catalog keys

Plan 20. Every replicated catalog table gets a primary key that leads
with the library, so two Libraries in one namespace can hold the same
id or the same relative path without touching each other's rows. At
the end of this plan the catalog's identity rule is: a row belongs to
one library, and the key says which.

## The problem

Every replicated table keys on a value that is unique only within one
`Library`: the item tables on `id`, `files` on a path relative to the
library root, `aliases` on `alias`, and `file_items` on `(path, item)`.
The tables replicate namespace-wide, one Corrosion cluster per
namespace. Two Libraries collide on the same relative path or a shared
provider id. Each upsert then flips the row's `library` column to the
last walker, and each library's prune can sweep rows the other library
still needs. The design always meant the library to scope a row; the
keys never said so.

A second, smaller identity gap rides along. A folder whose name slugs
to no letters keys as `movie:path:<year>` alone, so two such same-year
titles collide inside one library.

## The keys

Every replicated table keys on the library first:

- `movies`, `series`, `episodes`: `PRIMARY KEY (library, id)`
- `files`: `PRIMARY KEY (library, path)`
- `file_items`: gains a `library` column; `PRIMARY KEY (library, path, item)`
- `aliases`: gains a `library` column; `PRIMARY KEY (library, alias)`

The id strings themselves do not change: `movie:tmdb:603` and
`movie:path:the-matrix-1999` read the same as before. The scope lives
in the key, not in the string. The reverse-lookup indexes follow the
same rule and lead with the library, as every index in the schema
already does. Any future join from `episodes.series`, `aliases.item`,
or `file_items.item` to an item id must also match `library`; the
schema states this beside each column.

The `seen` table does not change. Each scanner creates it through the
write API on its own agent, so it never replicates and it is already
per-library.

Two upsert rules follow from cr-sqlite's model, where a change to a
primary-key column is a delete and a create:

- No `DO UPDATE SET` names a primary-key column.
- `file_items` becomes an all-key table, so its upsert is
  `ON CONFLICT (library, path, item) DO NOTHING`.

## The degenerate folder key

When the slug of a folder name has no letters, the folder key appends
a hyphen and the first eight hex characters of the SHA-256 of the raw
folder name. A name that slugs to nothing at all keys as the hash
alone. The hash is deterministic, so a re-walk reads the same id, and
a Latin folder name keys exactly as it does today.

## The migration

Corrosion refuses to change the primary key of an existing database,
so the new schema ships against fresh databases only. The catalog is
derived, so a fresh database costs one full walk.

The crossing release carried the schema revision in the claim name:
`<library>-catalog-v2`. The claim name sits inside the scanner pod's
spec, so the template hash changed, and the operator's own
stale-template replacement rolled every scanner onto a fresh claim.
The release after it returned to the plain name, once the old claims
were deleted: the operator deliberately holds no delete on claims, so
a person deletes them, or the owner reference releases them with the
Library. A cluster that crosses the key change in one jump must
delete its catalog claims first, because the operator adopts a claim
that already exists, old database and all.

## Considered and set aside

- **Prefixed key strings** (`<library>|<id>` in one column). Works
  everywhere a single-column key is required, but every reader must
  split the string, and the schema stops saying what the key means.
  Corrosion and cr-sqlite both support compound keys, so the legible
  shape wins.
- **One Corrosion cluster per Library.** Removes the collision by
  removing the sharing, but the namespace is the catalog boundary by
  design, and a cluster per Library multiplies gossip meshes.
- **An operator that deletes the old claim.** The operator writes to
  storage only what a person declared; deleting a volume of theirs is
  not its call.

## Proof

A drill on the local three-agent harness gates the build: the agents
accept the compound-key schema, the upserts and deletes apply and
replicate, the same id under two libraries coexists on every agent,
and a deliberately mixed-schema agent shows what a mid-roll overlap
does. The unit proof runs the real SQL against a real SQLite database
loaded with the shipped schema, and adds the cross-library cases: two
libraries with identical relative paths and provider ids, where one
library's walk and prune never touch the other's rows.

On the cluster, the proof is the reset itself: the pods roll onto
fresh claims, every library's first walk rebuilds it, the counts
return to their pre-roll values, and they hold across further walk
cycles. A count that flips between walks is the old bug's signature.
