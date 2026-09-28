# The music experience

Plan 11. It draws a music layout `liken` owns: the album art centered,
the track names as chapters on the scrubber, and one timeline for the
whole album. It builds on the display of
[plan 07](07-the-player-draws-its-own-display.md). When this plan
lands, a music `Play` looks like a music player, not a film with the
picture blanked.

## The problem

Through 07-e, a `music` item falls back to `mpv`'s default: `mpv` treats
the cover art as a video track and frames it, and the display draws a
title over it. The display does not control the art's size or position,
and it shows no tracks. A music player wants the art composed with the
tracks, and that means the display must own the whole frame, not
annotate `mpv`'s.

## The display owns the frame

The music layout blanks `mpv`'s video with `--vid=no`, so `mpv` frames
nothing and the display composes everything. This is the difference the
parent design names: for a film the display annotates `mpv`'s picture,
and for music the display owns the frame.

## The album is one piece of media

The player runs an album as one `mpv` item: an EDL timeline, one
segment per track, each segment with the track's title. `mpv` then
exposes one duration for the album and one chapter per track, at the
real track boundaries. Track selection is the film's own chapter stop
on the scrubber: `left` and `right` jump a track, the way they jump a
chapter in a film. Films and albums navigate with the same grammar,
one interface a viewer learns once. No separate track list exists, and
nothing captures input.

A track's boundary comes from its file's stated duration. A VBR `mp3`
without a proper duration header estimates, so a chapter boundary can
drift on such a file.

## The pod builds the timeline

Only the pod mounts the media, so everything that opens a file lives
in the player shim: the tags, the durations, and the art. The operator
never inspects media. The spec says what kind of thing each item is.

An item declares its shape in its presentation block, the way a video
item declares `hint: movie` or `hint: series`. A directory item with
`type: music` and `hint: album` expands in the shim into one written
EDL, still one playlist entry. The shim scans the directory's audio
files in name order, and each file's title tag becomes its chapter
name. A file item with the bare `type: music` passes through as one
standalone track. A `Play` mixes albums and tracks freely, and the
chapters appear exactly on the entries that have them.

The shim passes `--vid=no` when every item declares the music type,
because the flag is global to the run. A `Play` that mixes film and
music keeps video on, and that mix is outside this slice's proof.

## The layout

The header keeps its top-left place and carries the playing track: its
name from the current chapter's title, and the artist, album, and year
from the block. The top-right column keeps the clock, the activity
line, and the volume indicator, unchanged, so a viewer finds them in
the same corner on every screen. The album's cover centers in the full
frame between the header and the scrubber. The scrubber carries the
chapter marks, and the position runs over the whole album, because the
album is one timeline.

## Where the art comes from

The album has one cover, resolved from the EDL's first segment in
tiers. The bridge reads the picture embedded in that track first, with
the `dhowden/tag` library. When the track embeds none, a sibling
`cover.jpg` or `folder.jpg` serves. When neither exists, the frame
stays black and the header alone names the album. The header's text
fields have tiers of their own: the block's fields first, then the tags
`mpv` reads from the file, the way the title already falls back to
`media-title`.

The two shapes feed those tiers differently. A standalone track plays
as a plain file, so `mpv` reads its tags and the fallback tier works.
An album's timeline exposes no per-file tags to `mpv`, so its artist,
album, and year come from the item's block, and the chapter titles the
shim wrote carry the track names.

## The operator's part

The `Play` CRD gains the `artist`, `album`, and `art` presentation
fields, and `album` joins the hint values. The resolver rewrites `art`
the way it rewrites `logo`, so an `nfs://` cover reference arrives at
the pod as a mounted path.

## Set aside for this slice

* **A live queue.** Adding a track to a running `Play` over the bus is
  the live queue set aside for all of plan 07. An addition here means a
  rebuilt EDL, and this slice plays the timeline it started with.
* **A slideshow.** The `image` layout draws one photo, and a slideshow
  across a folder is a follow-on once the album timeline is proven,
  since both are the same "play a folder as one run" shape.
* **Visualizations.** An audio visualizer in place of the album art is
  its own design, and this slice draws the art the tiers name.

## How it will be proved

`local/music` joins `local/video` and `local/idle` as the workstation
proof path. It builds the EDL and the block from an album directory,
so the timeline, the chapters, and the art tiers can be seen without a
cluster.

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs an album as one timeline, and another runs
one file as a standalone track.

The drill checks each claim:

* The music `Play` shows the centered art and the scrubber over one
  album-wide position, not `mpv`'s framed cover art. The display owns
  the frame.
* The header carries the playing track's name, and it changes as
  playback crosses a chapter boundary.
* The chapter stop steps a track per press, each boundary lands on a
  real track start, and the header follows.
* The album plays gaplessly from one track to the next, because the
  album is one `mpv` item in one pod.
* A single file item with the bare `type: music` plays as a standalone
  track, and the header shows the tags `mpv` reads from it.
