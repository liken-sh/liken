# Art on screen

Plan 07-d, the fourth slice of [plan 07](07-the-player-draws-its-own-display.md).
It draws the display's first bitmap: the `logo` in the header, in place of
a film's title. When this slice lands, the display has a decode path from a
compressed art file to pixels on the screen, and a film with a `logo` shows
it instead of its title.

## The problem

Everything through 07-c is text and vector, drawn with `libass`. A `logo`
is a compressed image file, and `libass` cannot draw one. `mpv`'s
`overlay-add` places a bitmap, but it takes raw `bgra` only, and `mpv`'s
Lua has no image decoder. So the art needs a decoder between the file and
the screen.

`overlay-add` also places real screen pixels and does no scaling. The
display's text lives on a fixed 1920x1080 virtual canvas that `libass`
scales to the screen, so a logo placed by `overlay-add` cannot share the
text canvas. The display must place the logo in the screen's own pixels,
and the logo must already be decoded to the size it will occupy there.

## The bridge decodes the art

The bridge is the `command` sidecar, and it is the decoder, because it is
Go and `image/png` and `image/jpeg` are in its standard library. It reads
the logo file by the resolved path, scales it to the pixel size the display
asks for, and writes the `bgra` result to a volume the bridge and `mpv`
share. It hands the display a ready blob: the file path, the `w`, the `h`,
and the `stride`. The display places the blob with `overlay-add` and never
learns a file format.

The bridge scales with a small bilinear resampler it carries, so the
decoder adds no dependency. The module has none today, and a logo scale is
a few lines against the standard library's `image`.

The upstream `thumbfast` script solves the same decode problem by running a
second headless `mpv` to produce `bgra`. The bridge replaces that whole
process with one Go decode, because the Go process is already in the pod and
the second `mpv` is not.

## The logo field is a URI

The `presentation` block gains one art field this slice: `logo`, a `uri`.
It is a URI, not a path, so the resolver that turns a media `uri` into a
mount resolves it the same way. An `nfs://` logo mounts through a volume,
and an `https://` logo is fetched by the bridge.

The other art the parent design names, `clearart`, `backdrop`, and
`poster`, is not added here. Nothing draws it in this slice, because the
pause screen and the idle screen that would show it are set aside. Each
field arrives with the slice that draws it, so the schema carries only what
something reads.

## The resolver mounts a common ancestor

The logo adds a file beside the media, so the resolver changes how it
mounts. Today it mounts the exact directory of each `nfs://` media file.
With a logo beside the media, a `Play` references the media file and its
logo, and a series references more. This slice changes the resolver to
mount the common ancestor of all `nfs://` URIs on one server, and to
rewrite each file's path under that one mount. A film collapses to a single
read-only mount of its folder, which holds the media and the logo both.

The resolver now resolves the art URIs with the media URIs, in one pass, so
a logo and its media file share the mount. The block the operator bakes into
the pod carries the logo's resolved in-pod path, and an `https://` logo
stays a URL for the bridge to fetch. The mount stays read-only, so the pod
reads a wider subtree than one file but writes nothing.

This is the one change in the slice that edits the drilled playback path.
The resolver change ships with its own tests and lands before the decode
work, so a regression shows against the resolver, not as a black screen in
the drill.

## The display places the logo in real pixels

The display works in two coordinate spaces at once. Text stays on the fixed
1920x1080 virtual canvas, which `libass` scales to the screen. The logo
lives in the screen's real pixels, which `overlay-add` places without
scaling. The display reads the screen size from `osd-dimensions`, computes
the logo's box in real pixels, and converts its position from the virtual
canvas with the same scale `libass` uses.

The decode is a request and a reply, because only the display knows the
screen size and only the bridge can decode:

* The display computes the logo's target pixel size and asks the bridge for
  it, over a `script-message` the bridge reads on the IPC socket.
* The bridge decodes the current item's logo to that size, writes the
  `bgra`, and answers with a `script-message-to display` that carries the
  blob: the path, the `w`, the `h`, and the `stride`.
* The display places the blob with `overlay-add`, at the position it
  computed.

`header.lua` shows the logo in place of the title. When a logo blob is
present, the header draws no title line for a `movie` and no series-name
line for a `series`, and the logo takes that place. The second line, the
season and episode and date, stays as text. A `movie` or `series` with no
logo shows its title as it does today, so the header falls back with no
broken bitmap.

The display removes the logo overlay when the item swaps, before it asks for
the next item's logo, so one item's logo never lingers on the next. An item
with no logo leaves the overlay removed.

## Set aside for this slice

* **The pause screen and the idle screen.** The parent design draws
  `clearart` on a pause screen and a `backdrop` on an idle screen. Both are
  set aside, so `clearart` and `backdrop` are not added here. A later slice
  designs what a paused film and an idle `Play` show, and adds those fields
  when it draws them.
* **The `poster`.** A browse frontend uses it, and that is its own design.
* **The trickplay tile.** It is a bitmap too, and it rides this slice's
  decode path, but it is a crop from a sprite sheet that tracks the scrub
  cursor. It arrives in 07-e.
* **The music layout.** `music` stays on `mpv`'s default cover-art framing.
  The composed music experience is 07-f.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired DualSense.
A `Play` runs two items: a film that has a `logo` beside it, and a film that
has none.

The drill checks each claim:

* The film with a `logo` shows the logo in its header, not its title. The
  first bitmap is on screen.
* The film with no `logo` shows its title and the plain scrubber, and draws
  no broken bitmap. The header falls back.
* The `Play` advances from the first film to the second, and the logo swaps
  with the item. One item's logo never lingers on the next.
* The resolver mounts one directory for the film and its logo, not one per
  file. The pod's mount list holds a single media mount.
* The logo is sized right on the monitor, whose pixels are not the virtual
  canvas's. The display placed it in real pixels.
