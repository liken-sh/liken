---
title: Franchises
weight: 40
---

# The franchise file

A franchise is the films and series of one story, in the order the
story plays. A set is smaller: the chain of sequels a film's sidecar
names, in release order. A franchise is the long storyline those
chains sit inside, with the series that run between the films and
the prequels that play first. No metadata provider holds that order. TMDB collections
hold films only, in release order, and no source agrees on where a
series sits between two films. So a person or an agent writes the
order into a file, and the file is the truth.

A `Library` of kind `franchises` names a folder with one directory
per franchise. Each directory holds `franchise.yaml`, the franchise's
art under Kodi's names, and an `AGENTS.md` that says how the author
built the order. The scanner reads the YAML file and ignores the
rest. An agent that edits the directory reads `AGENTS.md` first, as
its context for the work.

The file validates against
[`franchise.schema.json`](https://library.liken.sh/franchise.schema.json). Put this line at
the top of the file, and an editor with YAML support checks it as
you type:

```yaml
# yaml-language-server: $schema=https://library.liken.sh/franchise.schema.json
```

## The parts

```yaml
name: Star Wars

sources:
  - https://www.starwars.com/news/star-wars-movies-and-series-guide

calendar:
  unit: years
  zero: Battle of Yavin
  before: BBY
  after: ABY

universe: Prime

eras:
  - name: Age of Rebellion
    from: -5
    to: 4

order:
  - movie: tmdb:1893
    title: "Star Wars: Episode I - The Phantom Menace (1999)"
    time: { from: -32, to: -32 }
  - series: tvdb:83268
    title: "Star Wars: The Clone Wars"
    seasons:
      - season: 1
        time: { from: -22, to: -22 }
      - season: 3
        episodes: [S03E01, S03E03-S03E22]
        note: Lucasfilm's order plays S03E02 before S02E16.
```

`name` and `order` are required. Everything else is optional.

| Part | What it is |
|---|---|
| `name` | The name the page shows. |
| `sources` | The pages the author read for the order and the times. The next author reads them first. |
| `calendar` | The franchise's own clock. Without it, the file holds an order and the page draws only the wall. |
| `universe` | The universe the story happens in. An entry with no universe of its own is in this one. |
| `eras` | Named stretches of the timeline, each with a span. Needs a calendar. Spans may overlap. |
| `order` | The story order, first to last. |

## Entries

An entry is one film or one series. A film is `movie` with a
provider id. A series is `series` with a provider id and, when only
part of it plays here, a `seasons` list.

A provider id is `scheme:id`. Films use `tmdb` and series use
`tvdb`, because those are the ids the movies and series libraries
write into the catalog. Another scheme is legal and resolves only if
some sidecar carries it.

A `series` with no `seasons` means the whole show. A season with no
`episodes` means the whole season, in aired order. An `episodes`
list holds codes like `S03E01` or ranges like `S03E03-S03E22`, in
the order they play. Specials are season 0. The same show may appear
again further down for the seasons that play after a film, and the
same season may appear again inside one show when the story order
cuts it into runs.

An episode code names the provider's numbering, and the provider
does not always number a season the way it aired. TheTVDB numbers
Firefly in aired order, so its pilot is `S01E11`, and a file that
plays the pilot first lists `S01E11` first. A library whose sidecars
came from a provider with a different numbering needs a different
list, so a franchise file names one provider's codes and the
directory's `AGENTS.md` says which.

The scanner walks the list depth first and writes one member row per
film or episode, with a position. A whole show or a whole season
expands to the episodes the catalog holds today, so a show that is
still airing fills in with no edit to the file.

Every entry, season included, may carry these:

| Field | What it is |
|---|---|
| `title` | The name the page draws when no library holds the member. Write the year after a film's title. |
| `time` | The span of the entry, as `{ from, to }`. Needs a calendar. |
| `universe` | Where this entry happens, when it is not the franchise's own universe. Not on a season. |
| `note` | Free text for the next author. It never reaches the page. |

## Times

A calendar needs a `unit`, either `years` or `days`. Without `zero`,
`before`, and `after`, the times are plain calendar years, which is
what the Marvel films count in. With them, the times count from the
named event, as Star Wars counts from the Battle of Yavin.

A `time` is `from` and `to` in the calendar's unit, and they are
equal for a story that stays in one year. A time on a show covers
every season under it, and a time on a season overrides it. A
flashback inside one film does not widen its span; the span is the
film's main setting. An entry with no time draws on the wall and not
on the axis.

## Members no library holds

A member need not be in any library. The page draws it as a gap
with its `title`, and the gap fills when the title arrives in any
library of the namespace. A gap whose title year is ahead of today
draws as coming, and any other gap draws as missing, so the wall is
also the list of what to go find.

## Authors

The first files are written from research. Later, an agent writes
the same file on a schedule. Both follow one rule: never remove a
member. Add and reorder, so a bad edit leaves a wrong order and
never a lost film.

Verify every provider id by fetching its page and reading the title.
Provider slugs mislead, so an id from memory is the one error that
survives every other check. Write the pages you used into `sources`,
and write the judgment calls into the directory's `AGENTS.md`.
