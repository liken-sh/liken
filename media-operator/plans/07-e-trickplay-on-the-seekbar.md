# Trickplay on the seekbar

Plan 07-e, the fifth slice of [plan 07](07-the-player-draws-its-own-display.md).
It puts a thumbnail on the scrub cursor: as the viewer seeks, the
seekbar shows the frame at the target time, cropped from a sprite sheet
the library made once. When this slice lands, a scrub shows where it is
going before it commits.

## The problem

07-a's scrubber shows a position and a time, but not the picture at that
position. A viewer scrubbing to a scene has to seek and watch, seek and
watch. A thumbnail on the cursor shows the scene before the seek
commits. `mpv`'s own live-thumbnail scripts decode the video ahead of
the scrub to make one, which a small `liken` machine cannot spare. This
slice shows a thumbnail from precomputed art instead, on the decode
path 07-d built.

## The trickplay is precomputed sprite sheets

The library generates the trickplay once, offline, as Jellyfin sprite
sheets: tiles of a fixed width in a grid, one thumbnail per interval.
A `320` sheet holds one hundred 320-pixel tiles in a ten-by-ten grid,
one tile every ten seconds. The display does not compute a thumbnail,
it reads a tile that already exists. This reuses the library's offline
work and spends no decode budget on the playback machine.

## The presentation block names the geometry

The `presentation` block gains its trickplay fields, because none of
the geometry is derivable from the JPEG:

* the sheet, as a `uri`, resolved the way the art is resolved in 07-d,
* the tile size, its width and height,
* the grid, its columns and rows,
* the interval, the seconds one tile covers.

## The bridge crops a tile

The display maps the scrub cursor's time to a tile: the tile index is
the time divided by the interval, and the row and column are the index
against the grid. The display asks the bridge for that tile, the bridge
crops it from the sheet to `bgra` on the shared volume, and the display
places it over the cursor with `overlay-add`. The crop reuses 07-d's
decode path, so this slice adds the tile arithmetic and the crop, not a
second decoder.

The tile appears only while scrubbing, and it tracks the cursor. As the
cursor glides, the display asks for each new tile and swaps the bitmap.
A crop is cheap, one region of one already-decoded sheet, so the tile
keeps up with the glide.

## Set aside for this slice

* **Live thumbnails.** Decoding the video for a thumbnail is what this
  slice avoids, on a small machine. It is not a fallback here.
* **A sheet the library did not make.** An item with no trickplay in its
  block shows the scrubber with no tile. This slice adds no way to
  generate one.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs a film with a trickplay directory beside it,
its geometry in the `presentation` block.

The drill checks each claim:

* A scrub shows a thumbnail on the cursor, cropped from the sprite
  sheet. The precomputed art is on the seekbar.
* The thumbnail tracks the cursor as it glides, and shows the frame at
  the target time.
* The thumbnail appears only while scrubbing, and clears when the OSD
  hides.
* A film with no trickplay in its block scrubs with no tile, and draws
  no broken bitmap.
