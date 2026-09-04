# Franchises

Plan 31. The third enrichment build from [plan 27](completed/27-enrichment.md).
A franchise is the films and series of one story in story order, a
home of its own on the screen, and a file a person or an agent writes
in a public repository.

## The problem

A set is what a movie sidecar names, one collection per film in
release order, and plan 22 draws it as a strip on the film's page. A
franchise is bigger and crosses kinds: the Marvel films with the
series that run between them, or Alien with its prequels in story
order. No sidecar carries it, the order is an opinion, and the members
are in different libraries on different volumes. So no member library
can hold it, and the scanner cannot derive it.

No online source holds story order either. TMDB collections hold films
only, in release order. Wikidata's `part of the series` property
carries a numbered order, not a timeline. The MCU timeline exists only
as marketing and fan articles, and it moved again in 2026. So the file
is the truth, not a cache of a source, and research writes it.

## The contract

- **A library kind.** A `Library` of kind `franchises` names a git
  repository, not a volume: `storage.git` holds a `url` and a `ref`,
  and the `storage` block becomes one of `claim` or `git` under the
  same CEL shape the kind blocks use. The files are a few hundred
  kilobytes, so nothing keeps a checkout. Each scan Job does a shallow
  clone of `ref` into an `emptyDir`, reads every `*/franchise.yaml`
  against the schema, writes a `franchises` table and a
  `franchise_members` table keyed by library like every other table,
  and exits with the checkout. `status.commit` holds the commit the
  last scan read, and a scan that finds the same commit skips the
  write. `spec.refresh` sets the period, as it does for the other
  kinds.
- **A failed fetch keeps the tables.** The other kinds scan a volume
  that is always there. This kind scans a server that is not. If the
  clone fails, the scan reports `Failed` on the `Library` and leaves
  the tables as they were. It never runs mark-and-sweep on an empty
  checkout, because a forge outage must not empty every franchise
  page for a day.
- **The files are public.** They live in a repository of their own,
  `tangled.org/guid.foo/fiction-franchises`, outside the `liken`
  organization, because a franchise is an opinion about a story and
  not a part of this operator. That repository carries its own copy
  of the schema and a README that defines the format with no
  reference to `liken`. Anyone may fork it or write their own, and a
  fork is a second `Library` with another `url`. Two libraries that
  both define one franchise are two rows, and the page draws both,
  the same way two movie libraries that hold one film do. The clone
  is anonymous over HTTPS, which tangled.org and GitHub both serve.
  A `secretRef` for a private repository waits until someone asks.
- **The file** holds the story order and nothing about release order,
  because release order comes from the members' own dates. The
  schema below is the whole contract between the file's author and
  the scanner.
- **The join.** A member is a provider id, and the screen resolves it
  through the `aliases` table to a row in whichever library of the
  namespace holds it. The scanner already writes every provider id a
  sidecar carries as an alias of the form `movie:tmdb:1771` or
  `series:tvdb:263365`, so a member line resolves by string match.
  The franchise scanner never reads another library's volume. A
  member no library holds draws as a gap in the order with its
  `title`, and it fills when the title arrives anywhere in the
  namespace. A gap whose `title` year is ahead of today draws as
  coming, and any other gap draws as missing, so the wall is also
  the list of what to go find.
- **The page**, from plan 22's primitives, is a vertical wall in
  story order: one row per entry, first to last, top to bottom. The
  rows follow the list and never the times, because the Marvel file
  runs from 1260 BC to 2028 with 120 of its 124 entries in the last
  twenty years, so a time scale is one dot and an empty rail. Time
  is a label on the row. A series run draws as one slot that says how
  many episodes it holds, so The Clone Wars in story order is one
  slot between two films and not thirty.
- **Universes are columns.** The franchise's own universe is the
  first column, and every other universe the file names is a column
  after it. An entry draws in the column of its universe. Two
  consecutive entries in different universes whose times overlap pack
  onto one row, so concurrent stories sit side by side. An entry
  whose `universes` names more than one draws as a banner across
  those columns, which is how No Way Home holds three Spider-Men.
- **Eras are a rail** on the left of the wall, one rotated bar per
  era over the rows it spans. The row an era covers comes from each
  row's `time` against the era's span, so the rail is derived and
  the author never writes a row. Eras overlap on purpose: a saga
  holds phases. Overlapping eras draw as nested rails, the wider one
  outer and the narrower one inner.
- **The remote.** Up and down move a row. Left and right move within
  a row when it holds more than one item. Left from the first column
  lands on the era rail, where up and down move an era and right
  returns to the first row of that era, which is how a person crosses
  124 rows. A press opens the film or the series. A toggle shows
  release order, sorted on the members' dates, as a plain list. The
  rail is a jump rail, and the wall's sort names it. Story order is
  the one sort a franchise has, so eras are its one rail. A later
  plan gives every long wall the rail its sort names: seasons on a
  series, years or letters on a movie wall, so a person jumps to 2011
  or to M the same way.
- **The way in.** A strip under a film's set strip draws the whole
  franchise in story order, centered on the current item, the way the
  set strip does, and a press on the strip's header opens the page.
  The home page of plan 26 gets a franchises row.
- **The manual.** The file's contract is a page on the docs site,
  `docs/content/docs/reference/franchises.md`, and a JSON Schema the
  site serves at `/franchise.schema.json`, the same schema the public
  repository carries. A file that opens with the
  `yaml-language-server` schema line validates in an editor before
  the scanner ever reads it, and the scanner enforces the same
  schema. Each franchise directory also holds an `AGENTS.md` with the
  author's method, sources, and judgment calls, which the scanner
  ignores and the next author reads first.
- **Art** comes from the franchise directory in the repository, and
  where the directory holds none, the enrichers of plan 30 copy it
  from the first member's art.
- **The author.** The first files are written by hand, from research.
  Later, an agent writes the same file on a schedule and pushes it
  to the repository, and the file is the only contract between the
  two. The author never removes a member. It adds and reorders, so a bad
  run leaves a wrong order and never a lost film.

## The schema

One `franchise.yaml` per directory. The scanner rejects a file that
breaks a rule here and reports it on the `Library`.

```yaml
# The name the page shows. Required.
name: Star Wars

# Where the author read the order. Optional, a list of URLs.
# The next author reads them first.
sources:
  - https://www.starwars.com/news/star-wars-timeline

# The franchise's own clock. Optional. Without it, the file holds an
# order and the page draws only the wall.
calendar:
  unit: years          # years or days
  zero: Battle of Yavin
  before: BBY          # the label after a negative time
  after: ABY           # the label after a positive time

# The universe the story happens in. Optional. An entry with no
# universes is in this one, and each other universe an entry names
# is a column of its own on the page.
universe: Earth-616

# Named stretches of the timeline. Optional, and only with a
# calendar. Spans may overlap. The page draws them as a rail beside
# the wall.
eras:
  - name: The High Republic
    from: -500
    to: -100
  - name: Age of Rebellion
    from: -5
    to: 5

# The story order, first to last. Required. Each entry is one film
# or one series. A series holds seasons, and a season may hold
# episodes, in the order they play. The scanner walks the tree depth
# first, and that walk is the story order.
order:
  - movie: tmdb:1893
    title: "Star Wars: Episode I – The Phantom Menace (1999)"
    time: { from: -32, to: -32 }
  - series: tvdb:83268
    title: "Star Wars: The Clone Wars"
    seasons:
      - season: 1
        time: { from: -22, to: -22 }
      - season: 2
        time: { from: -22, to: -21 }
      - season: 3
        episodes: [S03E01, S03E03-S03E22]
        time: { from: -21, to: -20 }
        note: Lucasfilm's order plays S03E02 before S02E16.
  - movie: tmdb:11
    title: "Star Wars (1977)"
    time: { from: 0, to: 0 }
  - movie: tmdb:557
    title: "Spider-Man (2002)"
    universes: [Earth-96283]
    time: { from: 2002, to: 2002 }
    note: An example from another franchise, since Star Wars has one universe.
  - movie: tmdb:634649
    title: "Spider-Man: No Way Home (2021)"
    universes: [Earth-616, Earth-96283, Earth-120703]
    time: { from: 2024, to: 2024 }
    note: One film in three universes draws as a banner across three columns.
```

The rules:

- An entry holds exactly one of `movie` or `series`. A `series` with
  no `seasons` means the whole show. A `season` with no `episodes`
  means the whole season. An `episodes` list holds `SnnEnn` codes or
  `SnnEnn-SnnEnn` ranges, in the order they play. The same show may
  appear again further down for the seasons that play after a film,
  and the same season may appear again inside one show when the
  story order cuts it into runs.
- An episode code names the provider's numbering, which is not
  always aired order. TheTVDB numbers Firefly in aired order, so its
  pilot is `S01E11`. A file names one provider's codes.
- The scanner expands the tree into one `franchise_members` row per
  film or episode, with a position. A whole show or a whole season
  expands to the episodes the catalog holds today, so an airing show
  fills in with no edit to the file.
- A provider id is `scheme:id`. Films use `tmdb` and series use
  `tvdb`, because those are the schemes the movies and series
  libraries write to `aliases`. Another scheme is legal and resolves
  only if some sidecar carries it.
- `title` is optional. The page draws it only when no library holds
  the member. Write it with the year for a film.
- A calendar needs `unit`. Without `zero`, `before`, and `after`,
  the times are plain calendar years, which is what the MCU counts
  in. With them, the times count from the named event, as Star Wars
  counts from Yavin.
- `time` needs a calendar. `from` and `to` are numbers in the
  calendar's unit, and they are equal for a story that stays in one
  year. A `time` on a level is the span of everything under it, and a
  lower level overrides it. A flashback inside one film does not
  widen its span. The span is the film's main setting.
- `universes` on an entry names every universe whose story the entry
  continues or joins, whether or not the camera goes there, and it is
  the franchise's own universe when absent. No Way Home names three,
  because it brings two other Spider-Man stories into the main one.
  An entry in one other universe draws in that universe's column. An
  entry that names several draws across their columns. The franchise's own `universe`
  stays one name, because a franchise has one home. An entry with no
  `time` draws in its row and joins no era on the rail.
- `note` is free text for the next author and never reaches the page.

## Proof

On `liken-1`, a `Library` of kind `franchises` over the public
repository, across the lab's movies and series libraries. The page
draws the order with its gaps and its eras, a film opens from it,
back returns to it, a member added to a library fills its gap on the
next scan, and a commit pushed to the repository reaches the page on
the next refresh. A scan with the forge unreachable leaves the page
as it was and reports `Failed`.

## What is set aside

A cluster-owner resource for a franchise, which plan 24 proposed. It
puts a truth in the cluster that no repository holds, and a catalog
rebuilt from the repositories would lose it. A `Library` holds only
the pointer, and the repository holds the truth.

A checkout on a volume that a person keeps by hand. The files are
small enough to fetch on every scan, and a volume adds a step the
operator can do itself.

Two orders in one file. Release order is derived, and a second
hand-written order can be a second franchise directory.

A review gate on the agent's writes. The stakes are an order on a
wall, the never-remove rule bounds a bad run, and a wrong file is
one edit.

## What is not decided

Which row the page shows when two libraries hold one film. Whether a
franchise may name a book or a game, which waits on those kinds.
Whether a franchise may reach another namespace, which the
one-cluster-per-namespace rule says no to today. Whether the scanner
fetches a tarball instead of a clone, which would drop `git` from its
image; today the archive URL differs per forge and carries no commit
id, so the clone wins. Whether a film with chapters set centuries
apart, like Predator: Killer of Killers, may carry more than one
span; today it holds one, and it joins every era the span crosses.
