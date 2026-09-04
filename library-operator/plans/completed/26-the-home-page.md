# The home page

Plan 26. Shaped on 2026-09-03. The first screen becomes a home page
that blends every library: a banner, a strip of what was released, a
strip of what arrived, a handful of strips the day picks, and the
libraries as the way into a wall. To draw those strips, every screen of
titles becomes one `Query` and one wall, and the catalog learns when a
file arrived.

## The problem

The browser opens on a list of libraries. That was the right first
screen for one movies library and one series library. It is the wrong
one for a house: a person who sits down wants what arrived this week,
what came out this month, the set they are halfway through, and a way
into any library, on one screen.

The catalog cannot answer "what arrived this week" today. Every item
table carries an `added` column, indexed by library, and the scanner
fills it from the folder's modification time. On a volume whose
importers stamp each file with its release date, that column is the
release date twice. The column is right and its source is wrong.

The catalog cannot answer "the Westerns" either. The genres of a title
are stored in its body column as JSON, in the order the provider gave
them, and a read over one genre is a scan of every body.

The browser holds three walls that are one wall. The library wall reads
one library and one kind, and fixes once what select opens. The
person page reads works across every library, and each slot carries its
own library and kind. A home page adds strips that read across every
library too, and a fourth copy of the wall is the wrong answer.

## The contract

**Every screen of titles is one `Query` and one wall.** A `Query` is a
value of a closed set of shapes. Each shape has a heading a person
reads, an order, a set of kinds it answers with, and a read the catalog
answers from an index:

```
Query
  Released  { kinds, fold }     newest release or air date first
  Added     { kinds, fold }     newest arrival first
  Genre     { name, order }     rank first, then released or added
  Person    { library, path }   every work of one person, as today
  Set       { library, id }     the members in release order
  Library   { library }         one library in sort order, as today
```

A strip draws the first slots of a query and a "see all" slot at its
end. Select on that slot opens the wall with the same query, and the
band across the wall carries the query's heading and its count. The
library wall is the wall with a `Library` query. The person page's wall
of works is the wall with a `Person` query. No wall fixes up front
what select opens: every slot carries its library and its kind, and
select opens the page for that kind, the way the person page does
today. A later plan adds `Franchise` when plan 31 lands, and `Search`
when the band's control comes alive. Each is one variant and one read.

A query is never a string of SQL. A string cannot be named in a
heading, cannot promise an indexed read, and cannot be a value in a
resource later. The closed set grows by one variant per plan.

**The arrival ledger.** An arrival fact, an enricher concern like
identity or credits, writes one file per folder, `.liken/arrival.yaml`,
with one entry per video file the folder holds: the file's path
relative to the folder and the time the file arrived. The file is in a
movie's folder and in a season folder, beside the ledgers the other
facts keep there. A path that has an entry is never written again. The
fact is the writer and not the walk, because the scan Job mounts the
volume read-only by plan 04's rule that the scanner writes nothing to
the volume, and the enrich Jobs already mount it read-write. The walk
reads the ledger, and the catalog's `files` table carries `arrived`,
the ledger's time for each file and zero where the ledger holds none,
so the fact's gap is every file with no entry. The fact runs minutes
after the walk that found the file, and it stamps the same change time
the walk read, so nothing a person sees moves between the two.

```yaml
# .liken/arrival.yaml
files:
  - path: The Matrix (1999).mkv
    at: 2025-03-14T02:11:09Z
```

The time of a new entry is the file's inode change time, and never the
clock. User space cannot set the change time, so when an importer
rewrites a file's modification time to the release date, the kernel
stamps the change time with the real moment of import. A file that is
seen for the first time on the day it arrives gets that day either way.
A file that was on the volume before the ledger existed gets its import
date where the change time still carries it, and the date of the last
sweep that touched it where it does not. The ledger makes either
durable against the next sweep. A person who knows better edits the
file.

**`added` comes from the ledger.** A movie's `added` is the arrival of
its main file, and an episode's is the arrival of its file. A series'
`added` is the earliest arrival among its episodes, and a set's is the
earliest among its members, the way each one's `released` is its
earliest today. The modification time feeds nothing. A file with no
entry yet takes its change time for the pass, so a walk that runs
before the fact still catalogs, and a read-only mount catalogs too.

**The genres table.** The scan writes a `genres` table with one row per
title and genre, in the order the sidecar lists them:

```sql
CREATE TABLE genres (
    library TEXT NOT NULL DEFAULT '',
    item TEXT NOT NULL DEFAULT '',
    rank INTEGER NOT NULL DEFAULT 0,
    genre TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library, item, rank)
);
CREATE INDEX genres_library_genre ON genres (library, genre);
```

The shape is the credits table's: the key is the title and a position,
and the index answers the range read. The order is kept because the
sidecar's first genre is the title's main genre, and a Westerns strip
puts the titles whose first genre is Western ahead of the ones that
list it fourth. The rows are derived from the sidecar, so the
mark-and-sweep prune covers them the way it covers credits. The table
answers both reads the home page makes: the titles of one genre in one
indexed range, and which genres exist with how many titles each, as one
grouped read over the same index. The facts that write genres into the
sidecar write these rows too under plan 34's rule, and until then the
next walk writes them. Corrosion's schema allows only `CREATE TABLE`
and `CREATE INDEX`, so an array column would be JSON text with no index
over its elements, and a genre read would be a scan again.

**The two recency queries fold episodes.** `Released` and `Added` read
movies and episodes, because an episode is the only thing about a
series that is ever new. Without a fold, ten episodes of one season
that land on one day are ten slots and the strip shows nothing else.
The fold reads the gap between an episode's release date and its
arrival:

```
fold
  Titles    every episode folds to its series
  Episodes  no fold; a season drop is ten slots
  Airing    an episode released within the window of its arrival
            stands alone; older ones fold to the series
```

A back-catalog season arrives years after it aired, and a person wants
one slot for the series. An airing season arrives the day after each
episode airs, and a person wants the episodes, the three from the
premiere included. The home page uses `Airing`, and the window is
fourteen days, a constant in the code beside the other numbers this
plan guesses at. A run of folded episodes from one series becomes one
slot for the series, at the newest date of the run. An episode that
stands alone is a slot with its still, captioned with its numbers and
its series' name, and select on it opens the series page with focus on
that episode.

The wall from a recency strip opens the same query with the fold set to
`Titles`. Every episode folds to its series, the wall stays all posters
at one ratio, and no wall ever holds a still. The strip and the wall
answer slightly different questions, "what is new" and "which titles
have something new", and that is the right difference.

**The home page.** The screen the shade lifts to, top to bottom:

- The banner. One title at a time, drawn the way a movie page's header
  is: the backdrop full bleed, the logo or the title, the facts line,
  and the tagline, in a frame that does not fill the screen, with a
  row of thin indicators for its place among the others. The banner
  holds one title from each strip the day drew, the newest release,
  and the newest arrival, in that order. A title with no backdrop is
  never in the banner. Left and right move across the banner, and
  select opens the title's page.
- The `Released` strip and the `Added` strip, always, in that order.
- The strips the day drew, from the pool below.
- The libraries, as one strip of the libraries themselves, each with
  its poster of a recent title, its name, and its count. Select opens
  the library wall. This strip is the floor: it is the one strip the
  page never loses.

A strip whose query answers nothing draws nothing. On a fresh cluster
the page is the libraries strip and the banner is empty, and the page
is still honest. The band across the top is the same band the walls
carry, so sort, filter, and search have one home on every screen. Up
from the banner reaches the band. Back from a wall or a page returns
to the home page at the same focus, and back from the home page asks
for the shade, as the libraries list does today.

A strip draws posters at 2:3 and stills at 16:9 in one row, left to
right, at one height. Nothing in a strip grows or crops to a ratio it
does not have.

**The pool and the seed.** The drawn strips come from a pool of
candidate queries: every genre, every person the catalog credits in
more than a few works, and every set. Each candidate carries a weight
from the count its read answers, so a genre that is the main genre of
two hundred titles draws more often than one that is the fourth genre
of twenty, and a person with fifteen works draws more often than one
with two. The page draws a fixed number of candidates from the pool
with a seed made from the date in the screen's own time zone, so the
page is the same all day and different tomorrow. A person who walks
away and comes back finds the page where they left it, and a person
who sits down on Saturday finds strips they did not see on Friday. The
draw is a pure function of the date and the catalog, so a wrong page
reproduces from those two alone. The number of strips and the weights
are constants in the code.

**The `Genre` order.** A genre's read orders by rank before date, so a
person who opens "see all" on Westerns sees the true Westerns first and
the crime films with a Western streak last. Within one rank the order
is released or added, as the query says.

## The build

Five changes, each a release, in this order. Each one leaves the
browser as fast as it is today and drills on `liken-1` before the next.

1. **The catalog.** The `genres` table, the arrival ledger, and
   `added` from the ledger. The scan writes the rows, the arrival fact
   writes the ledger, and the sweep covers the rows. Nothing in the
   browser changes.
2. **One wall.** The `Query` type and one wall screen fed by it. The
   library wall and the person page's works wall move onto it, and the
   band carries the query's heading. Every slot carries its library
   and kind. The libraries list stays the first screen.
3. **The home page.** The `Released` and `Added` queries with their
   fold, the strip primitive with mixed ratios and a "see all" slot,
   and the home page with those two strips and the libraries strip.
   It replaces the libraries list as the bottom of the stack.
4. **The day's draw.** The `Genre`, `Person`, and `Set` queries as
   strips, the pool, and the seed.
5. **The banner.** The header frame, its indicators, and the titles it
   holds.

## The local harness

`local/scan` walks a directory into the local three-agent catalog
through a read-only mount, so a run of the arrival fact from this
workstation records an error attempt and writes nothing. The ledger
write is
proved by the Go tests on a temporary directory, for the walk and for
the fact both. The schema change
needs `local/catalog` restarted, because the agents read the schema
directory at start. `local/browse` runs the
browser against that catalog and the real art, and its headless mode
captures each screen with `--script` and `--capture-at`, so changes two
to five iterate with a build and a restart, and no commit.

## What was set aside

A query as a string of SQL, for the reasons above.

The scanner as the ledger's writer. The walk is the first thing that
sees a file, and it was the writer at first. The scan Job mounts the
volume read-only, and that rule is worth more than the minutes the
fact runs behind the walk.

Genres as an array column. Corrosion's schema cannot index inside one.

A random banner. Moonfin's own documentation does not say how it picks
the titles in its media bar, and the layout it borrows from, MakD's
Jellyfin media bar, shows random items unless a hand-written list
replaces them. A banner with no connection to the strips under it is
decoration, and a random pick lands on titles with no backdrop.

Stills on a wall. The wall keeps one slot ratio, and the `Titles` fold
is why it can.

The fold window, the strip count, and the pool's weights as fields of
a resource. They are constants until the page has been lived with.

Which libraries a screen shows, and whether the rows are a fact of the
`Player`, the room, or the person. That open problem stays open. When
its resource arrives, it names rows by the `Query` shapes above, and
the closed set is the vocabulary it uses.

Continue watching. It is a strip with an empty read until plan 14
records positions, and it takes its place under `Added` then.

Search. The band's control keeps its place, and a `Search` query is one
variant when it comes alive.

Music and photos. They get rows here when they get screens of their
own.

## Proof

On this workstation first. A walk of a copy of a few titles writes
`genres` rows for each and `added` from the file's change time, the
arrival fact writes `.liken/arrival.yaml` beside each one, and a second
run of either rewrites nothing. Then captures at 1920x1080
from `local/browse` of the library wall on the one wall, a person's
works on it, the home page with a strip that mixes posters and stills,
the wall a "see all" opens, and the banner. The resident memory during
a walk from the home page into a wall, a page, and back is written
down beside plan 22's 153 MiB at rest.

Then on `liken-1`, from the X6: the scanners walk on the new schema
and every title gains its rows, the home page lifts with the shade,
the `Added` strip shows the last arrivals as episodes and series by the
fold, "see all" opens the wall and back returns to the same slot, the
day's strips draw and draw the same after a restart of the screen pod,
and the banner opens a page. The resident memory on the box is written
beside plan 22's 216 MiB.

## The drill, 2026-09-03 and 2026-09-04

Built in five waves as 2026.09.04-001 to -003, and drilled on `liken-1`
from the lab portable on both days. The scanners walked on the new
schema and every title gained its genres rows. The arrival ledger is
written by the arrival fact and not by the walk, because the scan Job
mounts the volume read-only; its first run stamped every video file
in both libraries. The `Added` strip shows the last arrivals as
episodes and series by the fold, and a 30-day window on `Released`
with a subtraction on `Added` keeps the two strips from showing the
same airing episodes. "See all" opens the wall and back returns to the
same slot. The day's strips draw the same after a restart. The banner
opens a page.

Two things the drill changed. Back to the home page took a second or
two, because every read ran in series on the frame thread; plan 36
took the reads off it. And the strips came up short when a season
drop folded a hundred rows to one slot; the recency read now pages
until it has filled twice what a strip shows.

The resident memory was not measured on either box. The numbers this
plan asked for are still owed beside plan 22's.
