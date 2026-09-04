# Genres on the home page

Plan 36. Shaped on 2026-09-04, from the first evening with plan 26's
home page on the lab portable. Three things: the home page reads off
the frame thread, so back from a page is instant; every genre gets a
strip on the home page and a page of its own; and "see all" on a strip
opens the page the strip is about, where one exists.

## The problem

Back from a page to the home page takes a second or two on the lab
portable. The browser rereads the whole home page on back and on every
lift of the shade: two paged recency reads with their folds, the pool,
four drawn strips, the banner's six details reads, and the libraries.
On this workstation that is a few hundred milliseconds of SQL over a
catalog of thousands. On the portable's CPU and eMMC it is the pause a
person feels. Every read runs on the frame thread, so nothing draws
until the last one answers, and the page a person left is the page
they want back at once.

Genres are the catalog's own vocabulary for a library, and the home
page shows four of them on a good day. A person who wants the Westerns
tonight has no way to them unless the day drew that strip.

"See all" on a strip opens the wall for the strip's query. For a person
the browser already has a better page: the person's own, with the
headshot, the dates, and the biography over the same wall. "See all"
on a person's strip skips it. A genre has no page at all.

## The contract

**The home page reads off the frame thread.** `Home` splits into a read
and an apply. The read is a pure function of a `Source` and today's
date: the rows, each strip's heading and items, and the banner's
titles. The apply takes that page and keeps focus where it was, the
way `reread` does today. The browser owns a reader: a thread over a
second `Source` of its own that answers one read at a time and wakes
the loop when the page is in hand. `Source::reader` yields that second
source, and a source that has no thread to give answers nothing, in
which case the browser reads in place. The sidecar answers a second
read-only connection to the same file. The test fixture and the sample
answer nothing, so every test reads in place and stays deterministic,
and one test proves the thread with a fixture that answers a clone.

**The home page rereads only when it is stale.** A change the source
reports marks it stale, and so does a date other than the one the page
was read on, because the day's draw is seeded by the date. Back and a
lift of the shade ask the reader for a page only while the home page is
stale. While the read is in flight the page a person left draws as it
was, and the fresh page lands on a later frame. Two asks in flight
collapse to one read and one more after it.

**Every genre is a strip on the home page.** A `Genres` row ends the
page, under the libraries. It draws one slot per genre in name order,
captioned with the genre and the count of titles that carry it. The art
is the poster of the newest-released movie or series that carries the
genre at any rank, across every library, and two genres may share one.
`Source::genres` is one read: the names, the counts, and that art. The
row has no "see all", because it is all of them.

**A genre has a page.** It is the wall with a `Genre` query and a head
over the grid, drawn the way a person's head is: the name at the title
size, and under it the counts, "N movies · M series". The band over a
headed wall carries the kind word, `Genre`, so the name reads once. A
select on a slot of the genres strip opens it. `Wall` holds an optional
`Head`, and the head is a function of the query and its answer, so the
wall for a `Genre` query is the genre's page wherever it opens from.
The head is where a later plan draws the root of a page's filters: on
the Horror page, Horror is the head and not a filter a person can
remove.

**"See all" opens the page the strip is about.** A person's strip opens
the person's page. A genre's strip opens the genre's page. A recency
strip opens the wall with every episode folded to its series, as
today, and a set's strip opens the set's wall. One function in the
screens module answers the screen for a query, so the home page and any
later strip open the same page.

## The build

Three changes, one release.

1. **The genre page and where "see all" goes.** `Head` on the wall, the
   `Genre` head from its answer, and the one function that opens the
   screen for a query. The person page's works stay on their own screen.
2. **The genres strip.** `Source::genres` on the sidecar, the sample,
   and the test fixture, and the `Genres` row that ends the home page.
3. **The read off the frame thread.** The split of `Home` into read and
   apply, `Source::reader`, the browser's reader thread, and the stale
   mark.

## The local harness

`local/browse` runs the browser against the local catalog, and its
headless mode captures each screen with `--script` and `--capture-at`.
The time a home read takes on this workstation is printed once per read
under `--stats`, so the number before and after is on the record.

## What was set aside

A read per strip, each on its own thread. One read on one thread is
enough to get off the frame thread, and one page landing at once is
what a person expects; strips that fill in one by one would shuffle
under focus.

Tuning each query. The reads are milliseconds each on this workstation
and indexed already. The pause is that they run in series on the frame
thread.

Genres as their own screen kind. A genre is a query with a head, and
the wall is its screen.

The person page as a headed wall. It could become one, and the head is
built so it can, but its headshot and biography come off the volume and
that is a change of its own.

The counts on the genres strip by library. The strip says how many
titles, and the page says how many of each kind.

## Proof

On this workstation: the home read's time under `--stats` before and
after, and captures at 1920x1080 from `local/browse` of the genres
strip at the end of the home page, the genre page a select opens, and
the person's page "see all" opens. Then on `liken-1`, from the X6: back
from a page lands on the home page with no pause, the genres strip is
there, and its posters open genre pages.
