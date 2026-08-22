# Art on screen

Plan 07-d, the fourth slice of [plan 07](07-the-player-draws-its-own-display.md).
It puts the art beside the media onto the screen: the `logo` in the
header, the `clearart` on the pause screen, and the `backdrop` behind an
idle `Play`. When this slice lands, a film shows its own art instead of
its title, and the display draws its first bitmap.

## The problem

Everything through 07-c is text and vector, drawn with `libass`. A
poster, a logo, and a backdrop are compressed image files, and `libass`
cannot draw them. `mpv`'s `overlay-add` places a bitmap, but it takes
raw `bgra` only, and `mpv`'s Lua has no image decoder. So the art needs
a decoder somewhere between the file and the screen. This slice adds
that decoder and the first bitmaps.

## The bridge decodes the art

The bridge is the decoder, because it is Go and `image/png` and
`image/jpeg` are in its standard library. It mounts the media share
already, to reach the media. So the bridge reads each art file by the
resolved path, decodes and scales it to `bgra` on the volume the bridge
and `mpv` share, and hands the display a ready blob with its `w`, `h`,
and `stride`. The display places the blob with `overlay-add` and never
learns a file format.

The upstream `thumbfast` script solves the same decode problem by
running a second headless `mpv` to produce `bgra`. The bridge replaces
that whole process with one Go decode, because the Go process is
already in the pod and the second `mpv` is not.

## The art fields are URIs

The `presentation` block gains its art fields, each a `uri`: `logo`,
`clearart`, `backdrop`, and `poster`. They are URIs, not paths, so the
resolver that already turns a media `uri` into a mount resolves them the
same way. An `nfs://` reference mounts through a volume, and an
`https://` reference is fetched by the bridge.

## The resolver mounts a common ancestor

The art multiplies the directories a `Play` references, so the resolver
changes how it mounts. Today it mounts the exact directory of each
`nfs://` file, which was one or two mounts for a `Play` of media files
alone. With art beside the media, a film references its media directory
and its art, and a season references far more. This slice changes the
resolver to mount the common ancestor of all `nfs://` URIs on one
server, and to rewrite each path under that one mount. A film collapses
to a single mount of its folder. The mount stays read-only, so the pod
reads a wider subtree than one file but writes nothing.

## The display places the bitmaps

The layout modules gain their bitmaps:

* `header.lua` shows the `logo` in place of the title for a `movie`,
  and falls back to the title when there is no `logo`.
* The pause screen shows the `clearart`, or the `backdrop` when there
  is no `clearart`.
* An idle `Play`, one with nothing playing, shows the `backdrop` and a
  clock.

The `image` type already shows its photo as `mpv`'s video track, so it
needs no bitmap here.

## Set aside for this slice

* **The trickplay tile.** It is a bitmap too, but it is a crop from a
  sprite sheet that tracks the scrub cursor, which is its own
  arithmetic. It arrives in 07-e on this slice's decode path.
* **The music layout.** The composed music experience, which places
  album art it owns, is 07-f. This slice leaves `music` on `mpv`'s
  default framing.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs a film that has a `logo`, a `clearart`, and a
`backdrop` beside it.

The drill checks each claim:

* The film's header shows its `logo`, not its title. The first bitmap
  is on screen.
* A pause shows the `clearart`. A film with no `clearart` shows the
  `backdrop`.
* An idle `Play` shows the `backdrop` and the clock.
* The resolver mounts one directory for the film and its art, not one
  per file. The pod's mount list holds a single media mount.
* A film with no art shows its title and the plain scrubber, and draws
  no broken bitmap.
