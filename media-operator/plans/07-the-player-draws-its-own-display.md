# The player draws its own display

Plan 07. It gives `liken` its own on-screen display, drawn over `mpv`
and tuned by the media type. A `Play` declares how it should look, the
display renders that declaration, and a remote navigates it. When this
plan lands, a film shows its own art and a trickplay seekbar, a remote
moves through the menus, and a music `Play` shows album art in place of
a scrubber.

## The problem

`mpv`'s built-in display is plain. Its controller and its text overlays
render in a subtitle style, and they are built for a mouse at a desk,
not a remote across a room. `liken` wants a display it owns: minimalist
in the project's manner, legible from a couch, navigated by a remote or
a gamepad, and different for a film, an episode, an album, and a photo.
The built-in display gives none of that, and no built-in option turns
it into that.

## The display is a script over mpv

The display is one script that `liken` writes, and `mpv` stays the
container. `mpv` runs a script in its built-in Lua interpreter, and the
script draws the display through `libass`, the renderer that also draws
subtitles. `libass` draws shapes, gradients, icons, and text, so the
seekbar, the menus, and the labels are all `libass` drawings. A second
call, `overlay-add`, places an RGBA bitmap on the frame, so a photo, a
poster, or a trickplay thumbnail reaches the screen, which `libass`
cannot draw.

Only the script is `liken`'s. `mpv`, its Lua runtime, `libass`, and
`overlay-add` are upstream and unchanged. Nothing wraps `mpv` and
nothing composites a second Wayland surface. The upstream script `uosc`
draws exactly this class of display through the same two calls, which is
the proof that the approach reaches the modern look. `liken` reads
`uosc` as a reference for the hard parts, the menu navigation and the
seekbar arithmetic, and depends on none of it.

## The play declares its presentation

A `Play` carries a `presentation` block, and the display renders only
what it declares. The block states the media type, `video`, `music`, or
`image`; a hint within the type, `movie`, `tv`, or `album`; and
references to the art and the trickplay that sit beside the media. The
display reads the block and resolves nothing on its own. It does not
read a library, scan a folder, or infer a type from a file name.

The art and the trickplay are files on the same share the media is on. A
film's folder holds `logo.png`, `clearart.png`, `backdrop.jpg`, and a
`.trickplay` directory of sprite sheets beside the `.mkv`. The pod
already mounts that share to reach the media, so the display reads the
art from the same mount by the paths the `presentation` block names.
There is no new volume and no new fetch.

## The bridge feeds the display

The `presentation` reaches the display over the wire the bridge already
drives. The bridge sidecar drives `mpv`'s IPC socket today, for input
and for the report. The `presentation` block travels the same socket:
the operator writes the block into the `Play`'s pod, the bridge reads
it, and the bridge hands it to the display script over the socket.

media-operator stays a router. It carries a richer `Play` and it
delivers that `Play`'s declaration to the pod. It does not resolve a
title into art, and it does not hold a catalog. Which program fills the
`presentation` block is a separate domain, stated below.

## The display is tuned by the type

The display draws a different layout for each media type, and the
`presentation` block selects it. A `movie` shows the film's `logo` in a
corner, a scrubber with trickplay thumbnails, and `clearart` on the
pause screen. A `tv` hint adds the show, the season, and the episode to
the header. `music` drops the scrubber's thumbnails, shows the album art
in the center, and lists the tracks. `image` shows the photo in a
minimal frame with no scrubber. An idle `Play` with nothing to show
draws the `backdrop` and a clock.

Each layout keeps the same minimalist manner: few elements, large type,
wide margins, sized for a screen the viewer reads from a couch. The type
does not change the engine. Every layout is `libass` and `overlay-add`,
and only the arrangement differs.

## Navigation is the remote's

The display is navigated by the command bus, not by a mouse. Plans 04
through 06 built the vocabulary and the bus that carry a remote's
presses to a `Play`. This plan adds the display actions to that
vocabulary: `menu` opens and closes the menu, `up`, `down`, `left`, and
`right` move the cursor, `select` acts, and `back` steps out. A `Keymap`
binds a controller's buttons to them exactly as it binds `play-pause`
today.

A press reaches the display the way a `play-pause` press reaches `mpv`.
The translator sidecar publishes the command, the bridge reads it, and
the bridge drives the display over the IPC socket. The display is one
more consumer of the command topic, so a gamepad's d-pad and a remote's
arrows both drive the menu with no new path.

## Trickplay on the seekbar

The seekbar shows the trickplay the library generated once, and does not
compute thumbnails live. The `.trickplay` directory holds Jellyfin
sprite sheets: tiles of a fixed width in a grid, one thumbnail per
interval, for example a `320 - 10x10` sheet of one hundred 320-pixel
tiles. The display maps the scrub position to a tile, crops that tile
from its sheet, and places it over the seekbar with `overlay-add`.

This reuses work the library did offline, and spends no decode budget on
the playback machine. `mpv`'s own live-thumbnail scripts decode ahead of
the scrub, which a small `liken` machine cannot spare. The precomputed
sheets cost one JPEG crop per scrub step and nothing more.

## Set aside for this plan

* **Which program fills the `presentation`.** Something writes the
  block: a person writing YAML, a scanner that reads `movie.nfo` and the
  art beside the media, or a later library operator. All three write the
  same block, and none is this plan's work. media-operator does not
  become a library.
* **A frontend that browses the library.** Choosing a film from a wall
  of posters is a frontend, and a frontend is its own design. This plan
  draws the display for a `Play` that already exists.
* **`uosc` as a dependency.** `liken` writes its own script and reads
  `uosc` only as a reference. Adopting `uosc` and reskinning it was
  considered and set aside, because its layout does not vary by media
  type or by the art beside the media, and the per-type display is the
  point.
* **Image slideshows and music queues.** The `image` and `music`
  layouts draw one item. A slideshow and a queue are follow-ons once the
  single-item layouts are proven.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs with a `presentation` block for a film that has
art and a `.trickplay` directory beside it.

The drill checks each claim:

* The film shows its `logo` and the movie layout, not the plain `mpv`
  display. The display `liken` owns is on screen.
* A `menu` press opens the menu, the d-pad moves the cursor, and
  `select` changes the audio track. The remote drives the display over
  the bus.
* A scrub shows a trickplay thumbnail cropped from a sprite sheet. The
  precomputed art is on the seekbar.
* A second `Play` with a `music` presentation shows album art and a
  track list, and no trickplay scrubber. The type tunes the display.
* The operator is killed. A press still opens the menu and moves the
  cursor, because the bridge and the translator run in the pod. The data
  plane outlives the control plane, as the whole design holds.
