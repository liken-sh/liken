# Franchises

Plan 31. The third enrichment build from [plan 27](completed/27-enrichment.md).
A franchise is the films and series of one story in story order, a
home of its own on the screen, and a file a person or an agent writes
on the volume.

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

- **A library kind.** A `Library` of kind `franchises` names a folder
  with one directory per franchise. Each holds `franchise.yaml` and
  the franchise's art under Kodi's names. Its scanner is small: it
  reads the files and writes a `franchises` table and a
  `franchise_members` table, keyed by library like every other table.
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
- **The page**, from plan 22's primitives: a header over the
  franchise's art, then the order as a wall, with a film and a series
  side by side and a season or an episode range as one slot.
  A series entry draws as one slot that says
  how many episodes it holds, so The Clone Wars in story order is one
  slot between two films and not thirty. The wall follows the list
  order, never the times. When the file declares a calendar, the eras
  head the stretches of the wall, and a time axis draws each member
  as a bar over its span, one era at a time, because Eyes of Wakanda
  spans three thousand years and every modern film is a dot on one
  axis. A toggle shows release order, sorted on the
  members' dates.
- **The way in.** A strip under a film's set strip names the franchise
  the film belongs to. The home page of plan 26 gets a franchises row.
- **Art** comes from the folder, and where the folder holds none, the
  enrichers of plan 30 copy it from the first member's art.
- **The author.** The first files are written by hand, from research.
  Later, an agent writes the same file on a schedule through a claim
  on the share, and the file is the only contract between the two.
  The author never removes a member. It adds and reorders, so a bad
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
# universe is in this one, and an entry with another one draws in a
# side lane on the axis.
universe: Earth-616

# Named stretches of the timeline. Optional, and only with a
# calendar. Spans may overlap. The page heads the wall with them.
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
    universe: Earth-96283
    time: { from: 2002, to: 2002 }
    note: An example from another franchise, since Star Wars has one universe.
```

The rules:

- An entry holds exactly one of `movie` or `series`. A `series` with
  no `seasons` means the whole show. A `season` with no `episodes`
  means the whole season. An `episodes` list holds `SnnEnn` codes or
  `SnnEnn-SnnEnn` ranges, in the order they play. The same show may
  appear again further down for the seasons that play after a film,
  and the same season may appear again inside one show when the
  story order cuts it into runs.
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
  lower level overrides it. A flashback inside one film does not widen its span; the span
  is the film's main setting.
- `universe` on an entry names where the story happens, and it is
  the franchise's own universe when absent. The axis draws the
  franchise's universe as the line and every other universe as a
  short side lane, so a story from another world sits beside the
  year it meets the main story, not among that year's films. An
  entry with no `time` draws on the wall and not on the axis.
- `note` is free text for the next author and never reaches the page.

## Proof

On `liken-1`, Star Wars and the MCU written from research into the
`Franchises` share, across the lab's movies and series libraries. The
page draws the order with its gaps and its eras, a film opens from it,
back returns to it, and a member added to a library fills its gap on
the next scan.

## What is set aside

A cluster-owner resource for a franchise, which plan 24 proposed. It
puts a truth in the cluster that the volume does not hold, and a
catalog rebuilt from the volume would lose it.

Two orders in one file. Release order is derived, and a second
hand-written order can be a second franchise directory.

A review gate on the agent's writes. The stakes are an order on a
wall, the never-remove rule bounds a bad run, and a wrong file is
one edit.

## What is not decided

Which row the page shows when two libraries hold one film. Whether a
franchise may name a book or a game, which waits on those kinds.
Whether a franchise may reach another namespace, which the
one-cluster-per-namespace rule says no to today. Where the schema
page lives on the docs site once an agent prompt has to cite it.
