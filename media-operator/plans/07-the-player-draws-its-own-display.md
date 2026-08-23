# The player draws its own display

Plan 07. It gives `liken` its own on-screen display, drawn over `mpv`
and tuned by the media type. A `Play` declares how it should look, the
display renders that declaration, and a remote navigates it. When this
plan lands, a film shows its own art and a trickplay seekbar, a remote
moves through the controls, and a music `Play` shows album art in place
of a scrubber.

## The problem

`mpv`'s built-in display is plain. Its controller and its text overlays
render in a subtitle style, and they are built for a mouse at a desk,
not a remote across a room. `liken` wants a display it owns: minimalist
in the project's manner, legible from a couch, navigated by a remote or
a gamepad, and different for a film, an episode, an album, and a photo.
The built-in display gives none of that, and no built-in option turns
it into that.

## The display is a script directory over mpv

The display is one script that `liken` writes, and `mpv` stays the
container. `mpv` runs the script in its built-in Lua interpreter, and
the script draws the display through `libass`, the renderer that also
draws subtitles.

The script is a directory, not one file. `mpv` treats a directory that
holds `main.lua` as a single script, and it adds that directory to
Lua's package path, so `main.lua` reaches its parts with plain
`require`. The manual names this the way to package a script of several
files. So the display is `main.lua` and a set of modules in one
directory, loaded as one `mpv` client.

One client, not several. Each `--script` is a separate `mpv` client in
its own thread, and two clients share no Lua value. They talk only by
`script-message-to`, passing strings. A display split across clients
would send a string over a thread boundary for every "the chooser is
open, so dim the scrubber", and the two would each own a separate
overlay and race on the z order. One client with many modules shares
state by a function call and draws through one overlay. So the display
is one client.

Only the script is `liken`'s. `mpv`, its Lua runtime, and `libass` are
upstream and unchanged. Nothing wraps `mpv` and nothing composites a
second Wayland surface. The upstream script `uosc` draws a display of
this class through the same calls, which is the proof that the approach
reaches the modern look. `liken` reads `uosc` and the `mpv` OSC scripts
as a reference for the hard parts, the seekbar arithmetic and the
chooser stack, and depends on none of them.

## What the two draw calls can render

The display draws with two `mpv` calls, and the split between them
decides the whole design.

`osd-overlay` with `format: ass-events` renders an ASS event string
against a virtual canvas whose size the script sets with `res_x` and
`res_y`. ASS is not only text. An event carries inline override tags
that form a small drawing language: `\pos` places an event anywhere on
the canvas, `\p1` switches it to a filled vector path with lines and
cubic beziers, `\clip` masks it to a shape, `\c` and `\alpha` set fill
and opacity, `\bord`, `\shad`, and `\blur` set outline, shadow, and
blur, and `\t` animates a tag over a time range. So a scrubber is a few
filled paths and a clip, a control's highlight is a rounded rectangle,
and a fade is one `\t`. The built-in `mp.assdraw` module builds these
strings, so the script writes `ass:round_rect_cw(...)` rather than the
raw path.

ASS cannot draw a photograph, and it has no layout engine. There is no
box model and no text flow the display would want, so the script
computes every coordinate itself in the units of the canvas it
declared.

`overlay-add` places a bitmap on the frame, which is how a poster, a
photo, or a trickplay thumbnail reaches the screen. It accepts one
pixel format, `bgra`, four bytes per pixel, from a file, a file
descriptor, or a memory address, with `stride == 4*w`. It decodes
nothing. A `.png` logo and a `.jpg` sprite sheet are compressed files,
and `mpv`'s Lua has no image decoder, so something must turn each image
into raw `bgra` before the script places it. That decoder is the
bridge, stated below.

## Icons come from an open-licensed font

The display draws its icons from an icon font in the player image, and
prints a codepoint in an ASS event to place one. This is how the
upstream OSC scripts draw their icons, and it costs one font file and
no per-icon vector path. The font must be open source under a liberal
license, because it ships in the player image. The alternative, drawing
each icon as an ASS vector path, was set aside: a font gives crisp
icons at any size for the price of one file, and a hand-drawn path set
is a second thing to maintain for no gain.

## The modules are organized by what they draw

The script is organized by domain, not by kind. There is no `draw.lua`
that draws for everything and no `state.lua` that holds everyone's
state, because a change to the seekbar would then touch every such
file. Instead one module owns one thing on screen, whole:

- `header.lua` draws the corner logo or the title, and for a `series` item
  the series, season, and episode line.
- `seekbar.lua` owns the fine scrubber: its geometry, its time labels,
  the playhead, and the trickplay tile it floats above the cursor while
  scrubbing.
- `chapters.lua` owns the chapter scrubber row.
- `subtitles.lua`, `audio.lua`, and `quality.lua` each own one control
  in the strip: its icon, its status, and its chooser.
- `prevnext.lua` owns the previous and next item controls.
- `clock.lua` owns the time-of-day cluster.

Two things belong to no single element, and each is its own concern,
not a junk drawer.

`theme.lua` is the project's visual vocabulary: the color palette, the
type scale, the margins, and the primitives every module draws with, a
rounded rectangle and a clip. It is the module that makes the display
look like `liken`, so it is a real domain.

`focus.lua` routes the remote. It holds which region has focus and
whether a chooser is capturing, and it sends each press to the right
module. It draws no pixels.

A supporting module is allowed where a concern is genuinely shared, for
example `drawing.lua` for the ASS string helpers or `loop.lua` for the
frame update. Each such module is named for its concern. None is named
`utils`, `common`, `shared`, `lib`, or `helpers`.

Each on-screen module is a table with a small contract. `available`
says whether it appears for the current item, so a `subtitles.lua` with
no subtitle tracks does not draw and does not take focus. `draw`
returns its ASS for the current state. A control adds `activate`, which
either fires an action or opens a chooser, and `handle`, which consumes
presses while its chooser captures.

## The Play declares its presentation

A `Play` carries its items as `spec.items`, and each item is a `uri`
and a `presentation` block. This replaces the earlier `spec.uris` list.
A bare `uri` with no `presentation` is the short form, for an item that
declares nothing. The parallel-list shape, a `presentation` list beside
`uris`, was set aside: two lists that must stay index-aligned can
drift, and one list of items that each carry their own `uri` cannot.

The `presentation` block carries only what `mpv` cannot already read
from the file:

- The type, `video`, `music`, or `image`, and a hint within the type,
  `movie`, `series`, or `album`. `mpv` cannot infer this, and the display
  must not guess it from a file name.
- The art, as `uri` references: `logo`, `clearart`, `backdrop`, and
  `poster`.
- The trickplay: the sprite-sheet `uri`, the tile size, the grid, and
  the interval. None of this is derivable from the JPEG.
- The `series` hierarchy: the series name, the season number, the episode number,
  and the episode title. A Matroska file rarely carries these.

The art and the trickplay are URIs, resolved the same way the media
`uri` is resolved. An `nfs://` art reference mounts through the same
volume the media mounts, and an `https://` reference is fetched. So the
art needs no new volume and no new mechanism, only the resolver that
already runs.

media-operator stays a router. It carries a richer `Play` and delivers
that `Play`'s declaration to the pod. It does not resolve a title into
art, and it holds no catalog. Which program fills the `presentation`
block is a separate domain, stated below.

## The display resolves each field in three tiers

The display fills each field from the first source that has it: the
`presentation` block, then an `mpv` property, then nothing. So a title
is `presentation.title` if the `Play` gave one, else `media-title`
from the file, else absent. A field that resolves to nothing does not
draw. There is no placeholder and no "Unknown".

The precedence puts the `Play` first on purpose. A library that feeds
`liken` supplies full, correct metadata, and the display trusts that
over a container's tags. The container's tags are the fallback for a
loose file that no library described. There is no fourth tier that
parses a file name, because that parsing is the library's job, and a
player that does it has started to become a library.

`mpv` already supplies, for free and per file, a set of properties the
`presentation` block therefore never carries:

- `media-title` and `metadata`, the container's title, and for music
  the artist, album, and track tags.
- `chapter-list`, the chapter titles and start times, which the chapter
  scrubber draws.
- `track-list`, each audio track and subtitle with its `title`, `lang`,
  `default`, `forced`, and `hearing-impaired` flags, which the audio
  and subtitle choosers list.
- `duration`, `time-pos`, and `percent-pos`, the seekbar's arithmetic.
- `video-params` and `current-tracks`, the resolution and which stream
  plays.

## The pod plays the whole batch

The pod plays the whole `spec.items` list, and it is one pod for the
session, not one pod per item. A play pod holds the display claim and
the sink claim for its whole life. One pod per item would release and
re-acquire those claims at every boundary, which is a black screen and
a re-negotiated audio sink between episodes, and a gap where another
`Play` could take the display. So `mpv` sees the full playlist and
plays it gaplessly, and the display swaps presentation when
`playlist-pos` changes.

The `presentation` still travels per item over the socket, not once at
startup. The bridge reads the block for the item that plays now and
hands it to the display, and it sends the next item's block when
`playlist-pos` advances. So a later live queue, where items arrive over
the bus into a running `mpv` through `loadfile append`, is an addition
to this path and not a rewrite of it. The live queue is set aside for
this plan.

## The bridge feeds the display

The `presentation` reaches the display over the wire the bridge already
drives. The bridge sidecar drives `mpv`'s IPC socket today, for input
and for the report. The operator writes the item's block into the pod,
the bridge reads it, and the bridge hands it to the display script over
the socket.

The bridge is also the image decoder, because `overlay-add` takes raw
`bgra` and `mpv`'s Lua cannot decode a file. The bridge is Go, and
`image/png` and `image/jpeg` are in its standard library. It mounts the
media share already, so it reads each art file and each trickplay sheet
by the resolved path, decodes and scales it to `bgra` on the volume the
two containers share, and hands the script a ready blob with its `w`,
`h`, and `stride`. The script places the blob and never learns a file
format. The upstream `thumbfast` script solves the same problem by
running a second headless `mpv` to produce `bgra`; the bridge replaces
that with one Go decode, because the Go process is already in the pod.

## The display is one stack of three focus regions

The OSD is hidden while a film plays, because the viewer sits ten feet
away and wants the picture. A press summons it, and a pause summons it.
When it is up, it is a vertical stack of focus regions, and `up` and
`down` walk between them. Each region uses `left` and `right` for its
own axis. This is the whole navigation model.

```
+--------------------------------------------------------------------+
|   +-----------+                                     20:14 -> 22:55  |
|   |  R A N    |                     (video)                         |
|   +-----------+                                                     |
|                                                                     |
|      1:04:22                              -1:36:45 . 2:41:07        |
|   *==================================O------------------------      |  fine scrubber   (seconds)
|   +--------+------+------------+---------+----------+-----------+    |
|   | Act 1  |Act 2 |  Act 3     |#Act 4##|  Act 5   |  Finale   |    |  chapter scrubber (chapters)
|   +--------+------+------------+---------+----------+-----------+    |
|                                                                     |
|   |< >|   Act 2 of 6                        (o)    [en]    (*)      |  control strip
|  prev next                                audio   subs  quality     |
+--------------------------------------------------------------------+
```

The three regions:

1. The **fine scrubber**. `left` and `right` seek by seconds, and the
   seek accelerates the longer a direction is held, so a tap nudges and
   a hold glides across an hour. The trickplay tile appears only here
   and only while scrubbing, and it tracks the cursor. Playback follows
   the cursor with a short debounce, so there is no separate commit
   step and no scrub sub-mode to exit.
2. The **chapter scrubber**. `left` and `right` step one chapter at a
   time, and the fine playhead follows. This row is present only when
   the file has chapters, and `up` and `down` skip it when it is
   absent.
3. The **control strip**. `left` and `right` move across the controls,
   and `select` acts on the focused one. `prev` and `next` fire at once
   and open nothing. `audio`, `subs`, and `quality` open a chooser
   tailored to what they pick.

`up` and `down` are never captured. They always move focus between the
regions, so the viewer is never trapped in a row. The only state that
captures input is an open chooser, and a chooser always closes on
`back`. That one rule keeps the model fluid: an open chooser is the one
place `back` means "close me", and everywhere else `back` dismisses the
OSD.

The main button is play-pause at every level, the cross on a DualSense
or the center button on a remote. There is no play-pause control in the
strip. Pausing summons the OSD, and playing hides it again after a few
idle seconds.

## The control strip is domain modules

Each control in the strip is its own module, drawn as an icon that
shows both its state and its status. `subtitles.lua` draws its icon
filled with `[en]` beside it when subtitles are on, and outlined when
they are off. A control decides for itself whether it appears:
`quality.lua` shows nothing when there is only one quality, and
`prevnext.lua` shows no previous control on the first item.

A control's chooser is tailored to what it picks, not a generic list.
The subtitle chooser is built for choosing a subtitle track, the audio
chooser for an audio track. This is why the strip is per-domain modules
and not one menu: a single boxy list serves none of them as well as a
purpose-built chooser serves each. A chooser captures `up`, `down`,
and `select` while it is open, and returns focus to the strip on
`back`.


## The time cluster shows five flavors

The display shows every time flavor a viewer might want, with no toggle
to switch between them, split across two places:

- On the fine scrubber: the position in the film, the time remaining,
  and the total length. `1:04:22` at the left, `-1:36:45 . 2:41:07` at
  the right.
- Top right, from `clock.lua`: the time of day now, and the time of day
  the film ends, as `20:14 -> 22:55`. The end time is `now +
  remaining`, computed each frame, so it tracks a pause or a seek. It
  is the one time the display computes rather than reads.

## The display is tuned by the type

The display draws a different layout for each media type, and the
`presentation` block selects it. The type does not change the engine.
Every layout is `libass` and `overlay-add`, and only the arrangement
differs.

- **movie** shows the `logo` in a corner, the three-region stack, and
  `clearart` or `backdrop` on the pause screen.
- **series** is the movie layout with the series, season, and episode added
  to the header.
- **image** shows the photo, which is `mpv`'s video track, in a minimal
  frame with no scrubber. Its `left` and `right` move across the items.
- **music** is deferred. For now `mpv` frames the cover art the file
  carries, as its own video track, and the display draws the title over
  it. A music experience that blanks the video and composes the art,
  the track list, and the queue is a later phase.
- **idle**, a `Play` with nothing to show, draws the `backdrop` and a
  clock.

## Trickplay on the seekbar

The seekbar shows the trickplay the library generated once, and does
not compute thumbnails live. The trickplay directory holds Jellyfin
sprite sheets: tiles of a fixed width in a grid, one thumbnail per
interval, for example a `320` sheet of one hundred 320-pixel tiles in a
ten-by-ten grid. The `presentation` block names the sheet, the tile
size, the grid, and the interval. The display maps the scrub position
to a tile, the bridge crops that tile from its sheet to `bgra`, and the
display places it over the seekbar with `overlay-add`.

This reuses work the library did offline, and spends no decode budget
on the playback machine. `mpv`'s own live-thumbnail scripts decode
ahead of the scrub, which a small `liken` machine cannot spare. The
precomputed sheets cost one crop per scrub step and nothing more.

## Navigation is the remote's

The display is navigated by the command bus, not by a mouse. Plans 04
through 06 built the vocabulary and the bus that carry a remote's
presses to a `Play`. This plan adds the display actions to that
vocabulary: `up`, `down`, `left`, and `right` walk the regions and move
within a row, `select` acts, and `back` closes a chooser or dismisses
the OSD. A `Keymap` binds a controller's buttons to them exactly as it
binds `play-pause` today.

A press reaches the display the way a `play-pause` press reaches `mpv`.
The translator sidecar publishes the command, the bridge reads it, and
the bridge drives the display over the IPC socket. The display is one
more consumer of the command topic, so a gamepad's d-pad and a remote's
arrows both drive the OSD with no new path.

## The build slices

This design is large, so it is built in slices, each its own plan
document, each usable when it lands. The slices iterate out from a
working core rather than build the whole display at once. Two seams in
the design order them.

The first seam is `mpv`'s properties against a new contract. The
scrubber, the chapters, and the track choosers all draw from what
`mpv` already reports, so they need no change to the `Play` and no
bridge decode. The `presentation` block is a later capability, not a
prerequisite. The second seam is vector and text against bitmaps.
Everything `libass` draws is one capability, and everything that needs
`overlay-add` and a decode to `bgra` is a separate, harder one.

The slices follow those seams:

* [07-a, The scrubber a remote summons](07-a-the-scrubber-a-remote-summons.md).
  The core. A script directory over `mpv` that draws a `liken` scrubber
  from `mpv`'s own properties, summoned by a press and scrubbed by a
  remote. It adds the navigation actions to the command vocabulary.
* [07-b, The stack and the choosers](07-b-the-stack-and-the-choosers.md).
  The vertical focus stack, the chapter scrubber, and the audio and
  subtitle choosers, all from `mpv`'s properties.
* [07-c, The presentation contract](07-c-the-presentation-contract.md).
  `spec.items` with a `presentation` block, the three-tier field
  resolution, per-item switching, and the text side of per-type tuning.
* [07-d, Art on screen](07-d-art-on-screen.md). The bridge decodes art
  to `bgra`, and the display places the `logo`, the `clearart`, and the
  `backdrop`.
* [07-e, Trickplay on the seekbar](07-e-trickplay-on-the-seekbar.md).
  The scrub cursor shows a thumbnail cropped from a Jellyfin sprite
  sheet.
* [07-f, The music experience](07-f-the-music-experience.md). The music
  layout blanks the video and composes the album art, the track list,
  and the queue.

## Set aside for this plan

- **The live queue.** Items arriving over the bus into a running `mpv`
  through `loadfile append` is a later plan. This plan plays a fixed
  batch declared in `spec.items`.
- **Which program fills the `presentation`.** A person writing YAML, a
  scanner that reads `movie.nfo` and the art beside the media, or a
  later library operator all write the same block. None is this plan's
  work, and media-operator does not become a library.
- **A frontend that browses the library.** Choosing a film from a wall
  of posters is a frontend, and a frontend is its own design. This plan
  draws the display for a `Play` that already exists.
- **`uosc` as a dependency.** `liken` writes its own script and reads
  `uosc` only as a reference. Adopting `uosc` and reskinning it was set
  aside, because its display does not vary by media type or by the art
  beside the media, and the per-type display is the point.
- **The music experience and image slideshows.** The `music` layout
  waits for the phase that composes the art itself, and a slideshow and
  a queue are follow-ons once the single-item layouts are proven.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs with a `presentation` block for a film that
has art and a trickplay directory beside it.

The drill checks each claim:

- The film shows its `logo` and the movie layout, not the plain `mpv`
  display. The display `liken` owns is on screen.
- A press summons the OSD onto the fine scrubber. `down` moves focus to
  the chapter scrubber, and `down` again to the control strip. `up` and
  `down` walk the regions and never trap the cursor.
- `left` and `right` on the fine scrubber scrub, and the seek
  accelerates on a hold. A trickplay thumbnail cropped from a sprite
  sheet tracks the cursor.
- On the control strip, `select` opens the audio chooser, and `select`
  in the chooser changes the audio track. `back` closes the chooser and
  returns focus to the strip.
- A second `Play` with a `music` presentation shows the cover art and
  the title, and no scrubber. The type tunes the display.
- The operator is killed. A press still summons the OSD and moves the
  cursor, because the bridge and the translator run in the pod. The
  data plane outlives the control plane, as the whole design holds.
