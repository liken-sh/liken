# The music experience

Plan 09. It draws a music layout `liken` owns: the album art centered,
the tracks listed, and the queue navigable, in place of the scrubber a
film shows. It builds on the display of
[plan 07](completed/07-the-player-draws-its-own-display.md). When this plan
lands, a music `Play` looks like a music player, not a film with the
picture blanked.

## The problem

Through 07-e, a `music` item falls back to `mpv`'s default: `mpv` treats
the cover art as a video track and frames it, and the display draws a
title over it. The display does not control the art's size or position,
and it shows no track list and no queue. A music player wants the art
composed with the tracks, and that means the display must own the whole
frame, not annotate `mpv`'s.

## The display owns the frame

The music layout blanks `mpv`'s video with `--vid=no`, so `mpv` frames
nothing and the display composes everything. It takes the album art
from the `presentation` block, the bridge decodes it to `bgra` on
07-d's path, and the display places it where the layout wants, centered
and sized by the display. This is the difference the parent design
names: for a film the display annotates `mpv`'s picture, and for music
the display owns the frame.

## The layout composes art, tracks, and queue

The music layout draws:

* the album art, centered, from the block's art `uri`,
* the track list, from the `Play`'s items, with the playing track
  marked and the artist and album from the resolved fields,
* the position within the track, as a slim scrubber, because a track
  still has a position even without a film's trickplay.

The queue is the `Play`'s `spec.items` list, so a music `Play` is many
tracks in one gapless run, the same list a film season uses. `left` and
`right` on the track list move through the queue, and `select` plays a
track.

## Set aside for this slice

* **A live queue.** Adding a track to a running `Play` over the bus is
  the live queue set aside for all of plan 07. This slice plays the
  fixed `spec.items` list.
* **A slideshow.** The `image` layout draws one photo, and a slideshow
  across a folder is a follow-on once the music queue is proven, since
  both are the same "play a list of one-item media" shape.
* **Visualizations.** An audio visualizer in place of the album art is
  its own design, and this slice draws the art the block names.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs an album as many track items, with album art
in the block.

The drill checks each claim:

* The music `Play` shows the album art centered and the track list, not
  `mpv`'s framed cover art. The display owns the frame.
* The playing track is marked, and the slim scrubber shows its
  position.
* `left` and `right` move through the queue, and `select` plays a track.
  The queue is the item list.
* The album plays gaplessly from one track to the next, because the pod
  holds the claims for the whole run.
