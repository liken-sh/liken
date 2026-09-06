# Search and the keyboard

Plan 39. Two keys the browser lacks, a search that answers as a person
types, and an on-screen keyboard for remotes without letters.

## The problem

The browser has six key words: up, down, left, right, enter, and
escape. A person three pages deep presses back three times to reach
the home page. A person who wants one title walks the library wall or
guesses a genre. A remote like the Fire TV X6 carries a full keyboard
that sends letters as ordinary evdev keys, and the browser ignores
every one of them. A DualSense has no letters at all. And a wall of a
thousand posters offers no way to narrow it.

## The contract

### Two keys

The browser adds two words to its key table, next to enter and escape:

- `KEY_HOMEPAGE` is **home**. It pops the stack to the home page. On
  the home page it does nothing.
- `KEY_SEARCH` is **search**. It opens the search wall with an empty
  field and the on-screen keyboard showing. On the search wall it does
  nothing.

Both are kernel key names, so a `Keymap` row binds any controller's
button to them with no operator change. The base table in
media-operator binds neither, because no controller in the base has a
button that means home. The local harness maps two keyboard keys to
them so a run on a laptop reaches both.

### Search is a wall

`Query` gains a variant, `Search { text: String }`. A search is a
`Wall` like every other list of titles, and it inherits the grid, the
focus, the band, the reread on change, and enter on a card. The band
heading is the text a person typed, and the count is the number of
hits. The source answers `Search` from an in-memory index, not from
SQL, and the `Query` module's comment changes to say that a query
promises a fast read, not an indexed SQL read.

Three ways open the search wall, and all three land on the same
screen:

- A letter or digit pressed on any screen opens the search wall with
  that character as the text. Back returns to the screen the person
  left.
- The **search** key opens it empty with the keyboard grid showing.
- An entry on the home page opens it the way the search key does.

On the search wall, a letter, digit, space, or backspace edits the text
and rereads the wall. Results change under the typing, on the same
screen, with no submit. Escape with text in the field clears the text
first. Escape with an empty field leaves the wall.

### The index

The sidecar source owns the index, because the source owns the replica
and already follows the updates feed. The index holds one folded entry
per searchable string. Folding is lowercase, diacritics stripped, and
split on every character that is not a letter or a digit.

The strings, in rank order of where a match lands:

1. the title of a movie, series, set, or franchise
2. an alias or original title
3. a contributor's name
4. an episode's title
5. a description: a movie's, a series', or an episode's plot

The kinds of match, in rank order:

1. the whole string
2. a prefix of the first word
3. a prefix of a later word
4. a substring inside a word

The query text folds the same way. Every word of the query must match
the same item. An item's rank is the best where and the best how across
its strings, ordered by where first and how second. Ties break by kind
(movies and series before sets and franchises, then episodes, then
people), then by release date newest first, then by sort key. A person
who matches opens their page, and an episode opens its series' page.

The index rebuilds from the replica when the updates feed signals a
change, after a quiet period of about two seconds, on the reader
thread and never on the frame. It rebuilds whole, because a folded
index is cheap to build and a partial update is a second source of
truth. The stats line reports the index's size in bytes and its entry
count. A test builds the index over a sample catalog scaled to the
size of a large collection and asserts it stays under 20 MB.

Music is not in the catalog yet. When it arrives, an album's title
joins rung 1, an artist's name joins rung 3, and a track's title joins
rung 4.

### The keyboard

Two pieces in the browser, promoted to a shared crate when a second
app wants them:

- A `TextField` holds the text and takes one kernel key name at a time:
  a letter, a digit, `KEY_SPACE`, or `KEY_BACKSPACE`. It answers
  whether the text changed. It knows nothing of screens or of the grid.
- A `Keyboard` is a grid of letters, digits, space, and backspace.
  Up, down, left, and right move its focus. Enter presses the focused
  cell into the `TextField` by the cell's key name, so a letter from
  the grid and a letter from a physical keyboard take one path.

The grid shows when the search wall opens from the search key or the
home page entry. It hides when a physical letter arrives, because a
person with an X6 never needs it. A d-pad press on the search wall with
the grid hidden moves focus in the results, and up from the first row
reaches the band, where enter shows the grid again.

## What is set aside

- **A filter control on the walls**, built here and removed by plan 40.
  In use it was a wall of buttons over a wall of posters, and it
  answered nothing search and a genre wall did not. Plan 40 puts a
  jump rail and a four-way order cycle in its place.
- **A sort control.** Every query names its order. Plan 40 gives long
  walls one button that cycles four orders, and no more.

- **An FTS5 table replicated through Corrosion.** Corrosion's schema
  allows only `CREATE TABLE` and `CREATE INDEX`, so no virtual table
  replicates.
- **A browser-owned FTS5 file next to the replica.** A second database
  is a second thing to be stale or torn on a one-gigabyte machine, and
  the collection is thousands of rows, not millions. The folded
  in-memory index answers a keystroke in a few milliseconds.
- **A replicated `search_terms` table the operator writes.** Every
  replica would carry rows for a word split only the browser needs,
  and a ranking change would be an operator release and a re-scan.
- **Search scoped to a wall**, a `within` on `Search` that keeps only
  the hits in the wall's set. It is cheap, but it is a fourth way into
  search. It stays a named seam.
- **Letters that open search only from the home page.** Every TV app
  with a keyboard opens search from anywhere, and back returns.

## Proof

- On a local run, a letter on the home page opens the search wall with
  that letter, and each further letter changes the wall on the same
  screen. "batman" ranks a movie titled Batman over a series, and both
  over an episode whose plot names him.
- The search key on a DualSense opens the empty wall with the grid, the
  d-pad spells a word, and the results change with each pick.
- The home key from three pages deep lands on the home page.
- The stats line shows the index size under 20 MB on a catalog the size
  of the largest library on the testbed.
- On `liken-1`, an X6 bound by a `Keymap` row reaches home and search,
  and typing on its keyboard searches.

## The drill, 2026-09-06

Built in the working tree over two days and drilled on this
workstation from `local/browse` against the local catalog. A letter on
any screen opens the search wall seeded with it, and each further
letter changes the wall on the same screen. "batman" ranks the film
over the series and both over an episode whose plot names him. The grid
opens from the search key and hides at the first physical letter. Home
from three pages deep lands on the home page, and home on the home page
hops to the banner. The index over the local catalog builds in under a
second, and the scale test holds it under 20 MB at about 100,000 items.

Two things the drill changed. A person in two libraries appeared twice
in the results, so the index folds contributor rows by path. The local
harness bound the letter q to quit, so on a laptop Escape quits now,
the forward slash is back, and the backtick is home.

The `liken-1` drill, with `Keymap` rows that bind the X6's home and
search buttons, is still owed.
