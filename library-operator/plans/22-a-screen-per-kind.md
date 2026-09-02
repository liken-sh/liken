# A screen per kind

Plan 22. The media browser gets a screen designed for each kind, a page
for a movie, a page for a series, and the sets a movie belongs to. At
the end of this plan the browser is as fast as it is today and looks
like a media system.

## The problem

The browser draws two things, a wall of posters and a list of rows, and
a table maps each kind to a stack of those two. That table was the
right first cut. It is the wrong shape for what the browser has to
become. A movie plays from the wall with nothing shown about it. A
series is three lists that look like every other list. The catalog
holds much more than the browser reads: each movie row carries the
plot, the tagline, the cast with roles, the directors, the writers, the
studios, the genres, the content rating, and the set it belongs to.
Each series row carries the plot, the cast, and the creators. The
`files` table names every art file by role: poster, backdrop, logo,
banner, thumb, clearart. None of that reaches the screen.

Two properties of the browser today have to survive. It is fast on the
box: a wall of thousands of posters scrolls with no wait, and a press
draws the next frame at once. And it takes six buttons: up, down, left,
right, select, and back.

## The contract

**A kind is a plugin in the scanner and a screen design in the
browser.** The scanner side stays data: how to walk a root and how to
read that kind's sidecars. The browser side is code, on purpose. The
browser holds shared drawing primitives, and a screen per kind composes
them. The primitives this plan needs are a wall of art slots with a
slot ratio, a header band over a backdrop, a text block, a button row,
and a strip of posters with one marked. A later kind adds a screen and,
where it needs one, a primitive. It adds no row to a table.

**The wall gets a band.** A movies wall and a series wall draw one thin
band across the top: the library's name and its count at the left, and
three controls at the right for sort, filter, and search. Up from the
first row of posters reaches the band, and left and right move across
the three controls. The controls draw dimmed, and select on any of them
does nothing, because none of the three exists yet. The band is the
reserved place for them, the same place on every wall. Every poster
carries a one-line caption under it, muted. The focused poster's
caption is bright and carries the facts: the title, the year, the
runtime, and the content rating, where the row carries them.

**Focus is an outline.** The focused slot does not grow. It carries a
thick stroke of the accent outside its edge and a bright caption. A
grown slot needs a second decode at a second size, and the flash while
that decode lands is what a person sees. One decode per slot size
draws with no flash, and a later plan can add motion over the same
decoded image.

**Select on a movie opens its page.** The page draws the movie's
backdrop full bleed, dimmed toward the lower left where the text sits.
Over it, top to bottom: the logo, or the title in large text where the
item has no logo; one line of facts, the year, the runtime, the content
rating, and the genres; the tagline; the plot, cut to four lines with a
fade; the button row; the set strip; then the credits, directed by and
written by, and the cast as names with roles. The cast is text and takes
no focus. Plan 14 adds people, and the cast row becomes focusable then.

**The button row.** Play, and Trailer where the `files` table holds a
file with the `trailer` role for the item. Focus lands on Play when the
page opens, so a film is two presses from the wall as it is today.
Select on Play publishes the play request plan 08 defined. Select on
Trailer publishes the same request with the trailer's path in place of
the main file, the movie's presentation, and no trickplay. Left and
right move across the row. Down from the row reaches the set strip
where the movie has a set, and the credits where it has none.

**The set strip.** Where the movie belongs to a set, one strip under
the buttons shows the whole set in release order, with the set's name
as its heading. The current film sits in its place at full brightness
with the accent stroke, the same mark the wall gives focus, and its
siblings sit dimmed. Left and right move across the strip. Select on a
sibling replaces the page with that film's page, and does not push a
new one, so back from any film in the set returns to the wall. The
strip is a way to move inside a set, the way a season divider is a way
to move inside a series, and not a screen of its own.

**Pages scroll with focus.** A page is one stack of blocks over a fixed
backdrop, and the stack scrolls so the row that holds focus and the
blocks under it are in view. On the movie page, focus on the buttons
shows the page from its top, and focus on the strip brings the strip
and the credits under it into view. On the series page the episode
wall scrolls under the fixed header by the same rule. No block a focus
row can reach is ever cut at the bottom of the frame.

**Text sits on a scrim, and art never sits on art.** Every page draws a
scrim behind its text column, dark at the left edge and clear by about
six tenths of the width, over any backdrop. Posters and stills draw on
the ground and never over a backdrop. The renderer draws a canvas's
fills, then its images, then its text, whatever order the code drew
them in, so a page is a stack of canvases in depth order: the backdrop,
the scrim, then everything else.

**Sets in the catalog.** The scanner writes a `sets` table with the same
header columns the other item tables carry, and a `set_id` column on
`movies` that names the set's id, indexed. The id is provider scoped
like every other item: `set:tmdb:<id>` from the `tmdbcolid` attribute
Jellyfin writes on the `<set>` element, and `set:name:<slug>` where a
sidecar names a set with no id. The set's `released` is its earliest
member's, its `art` is that member's poster, and its `body` is empty.
A set is derived from its members, so the mark-and-sweep prune covers
it: a set whose last member leaves the library leaves with it. The
browser's set strip is then one indexed read and not a scan of the body
column.

**Select on a series opens its page.** The page is one screen and not
three. The header draws the series' backdrop, its logo or title, the
facts line with the year, the season count, and the content rating,
the tagline, and the cast. Focus is always on an episode, and the
header shows that episode's numbers, name, runtime, and plot. Under the
header, on the ground and not on the backdrop, a wall of episode stills
at 16:9 with a caption under each still, in aired order. A season
divider is one line before each season's first row: the season's name
at the left and its year at the right, the year of its first aired
episode. The season poster does not appear. Left and right move inside
a season, and up and down cross the dividers, so a divider is visual
and not a stop. The header stays fixed, because the focused episode's
facts are in it, and the wall scrolls under it. Select plays the
episode and the rest of its season, as plan 08 defined.

**Art by role.** The pages read an item's backdrop, logo, and trailer
from the `files` table through `file_items`, by role. An item with no
backdrop draws the page over the ground color. An item with no logo
draws its title in text. A still is the episode row's own `art`.

**Prefetch on rest.** A page has to open with its backdrop drawn, or
the browser stops feeling fast. When focus rests on a wall item for a
short moment, the poster store decodes that item's backdrop at page
size, so select finds it in the cache. The store's byte budget grows
to hold a few backdrops beside the posters, and the proof measures
what that costs in resident memory. A backdrop that is not decoded
when the page opens draws when it lands, the way a poster does.

**The three statements.** The design and the code say today that a
kind adds a row to a table and no drawing code. This plan replaces
those statements in `plans/00-design.md`, in `plans/13-more-kinds.md`,
and in the browser's level module with the principle above.

## The local harness

`local/browse` already runs the browser against the real scanned
catalog and the real art on this workstation, so every screen in this
plan iterates with a build and a restart, and no commit. The schema
change needs `local/catalog` restarted, because the agents read the
schema directory at start, and `local/scan` run once more so the sets
land. The headless mode captures each new screen with `--script` and
`--capture-at`, and those captures are the first proof.

## What was set aside

Franchises. A franchise, the whole MCU as one item in one order, has no
source in any sidecar, and its order is an opinion and not a fact of
the files. It is a screen kind of its own and a later plan.

A set as one poster on the wall. Jellyfin offers it as an option. It
hides the film a person scans for, and it needs set art the volume does
not hold. The wall stays flat, and the set shows itself on the page.
When filters arrive, "by set" is one facet.

A set page. The strip does its job on the movie page, and a franchise
page later is built from the strip.

An episode page. An episode has a plot and a runtime, and the series
header shows both when the episode has focus.

Season art. The volume holds season posters, and a season's year is the
one fact a person needs at the divider. Speed and clarity win.

Cast headshots. Kodi's convention is an `.actors` folder of thumbs
beside the film, and whether Jellyfin's saver writes them is unchecked.
The cast is text for now.

Sort, filter, and search. The band reserves their place.

People pages, the home page that blends every kind, and the music
screens.

## Proof

On this workstation first, from `local/browse`, with captures at
1920x1080 of the movies wall with its band, a movie page with a set
strip, a movie page with no set and no logo, and a series page scrolled
into its second season. The resident memory during a walk from the wall
into a page and back, repeated across the wall, is written down beside
plan 07's 130.9 MiB.

Then on `liken-1`, from the X6: the wall's band takes focus and gives
it back, select opens a movie page with its backdrop already drawn,
Play starts the film, back returns to the wall at the same focus, a
sibling in the set strip opens its page and back from it returns to
the wall, a series opens to its page and a held down crosses the season
dividers, and select on an episode plays it. The resident memory on the
box during the same walk is written beside plan 07's 92 MiB.

Drilled on this workstation on 2026-09-02, from the local catalog and
the real roots, with headless captures at 1920x1080 of every screen.
The drill found six defects, all fixed the same day. A raster over 2
MiB never drew, because the renderer hands it to a worker thread and
the browser draws no later frame; large art now draws as bands under
the cap. Logos drew cropped, because the decoder covered every box;
logos now fit. Every frame re-uploaded every poster, because each frame
built a new handle; a cached decode now keeps its handles. Fills drawn
over the backdrop vanished, because a layer draws fills, then images,
then text; a page is now a stack of three canvases. The focused line
overlapped the row below, and a crowded page cut its credits off at the
foot of the frame; captions on every slot and the scroll rule replaced
both. Resident memory had risen to 435 MiB on a wall where focus rests
at every step, against plan 07's 131 MiB, and the cause was decode
transients held in the allocator's arenas and not the cache. With the
allocator thresholds pinned and one page-size decode in flight at a
time, the same walk peaks at 186 MiB and rests at 153, and a walk
through ten movie pages peaks at 156. The `liken-1` drill is owed.
