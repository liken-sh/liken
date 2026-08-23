# Trickplay on the seekbar

Plan 07-e, the fifth slice of [plan 07](07-the-player-draws-its-own-display.md).
It puts a thumbnail on the scrub cursor. As the viewer seeks, the seekbar shows
the frame at the target time, cropped from a sprite sheet the library made once.
When this slice lands, a scrub shows where it is going before it commits.

## The problem

The scrubber from 07-a shows a position and a time, but not the picture at that
position. A viewer who scrubs to a scene seeks, watches, seeks again, and
watches again. A thumbnail on the cursor shows the scene before the seek
commits.

`mpv`'s own live-thumbnail scripts decode the video ahead of the scrub to make
one. A small `liken` machine holds a 1GB memory envelope, and a second decoder
does not fit in it. This slice shows a thumbnail from precomputed art instead,
on the decode path 07-d built.

## The trickplay is precomputed sprite sheets

The library generates the trickplay once, offline, as Jellyfin sprite sheets.
Beside a film `X.mkv` sits a directory `X.trickplay`. Inside it, one directory
names the layout, for example `320 - 10x10`. The `320` is the tile width in
pixels, and the `10x10` is the grid, so one sheet holds one hundred tiles. The
sheets are `0.jpg`, `1.jpg`, and so on, each a full grid, filled row by row.

The display does not compute a thumbnail. It reads a tile that already exists.
This reuses the library's offline work, and it spends no decode budget on the
playback machine.

## The bridge reads the geometry, the Play declares the interval

Almost every number the crop needs is a fact on the disk, so the bridge reads
it and the `Play` declares nothing for it:

* The tile width and the grid are in the layout directory name, `320 - 10x10`.
* The tile height is the sheet height divided by the rows. A `10x10` sheet that
  is `1360` pixels tall has a `136`-pixel tile, the film's scope aspect, not
  16:9. So the bridge reads the height from the sheet and assumes no aspect.

The one number that is not on the disk is the interval, the seconds one tile
covers. Jellyfin writes no manifest beside the sheets, and the last sheet is
padded with black rather than cropped, so the tile count cannot be read back
from the files. So the interval is a declared value. `PlaySpec` gains a
`trickplayInterval`, and it defaults to `10s`, the Jellyfin default the library
uses. The pod carries it to the bridge as an environment value.

This is the change from the first draft of this plan, which declared the tile
size, the grid, and the interval all in the block. The geometry is on the disk,
so the block declares only what is not: one reference, and the interval on the
`Play`.

## The block declares the reference

The `presentation` block gains one field, `trickplay`, the reference to the
`X.trickplay` directory. It resolves the way the `logo` resolves in 07-d. The
directory sits under the same media tree as the film, so the common-ancestor
mount already covers it, and the resolver rewrites the reference with the rest.
An item with no `trickplay` shows the scrubber with no tile.

## The bridge crops a tile

The display maps the scrub cursor's time to a tile, and the bridge crops it:

1. The display sends the bridge the cursor time in milliseconds and the pixel
   size it wants, over the request path 07-d built.
2. The bridge divides the time by the interval for the tile index, divides the
   index by the grid for the sheet and the cell, and takes the row and the
   column from the cell.
3. The bridge decodes that sheet once and holds it, crops the cell to the tile,
   scales the tile to the requested size as `bgra` on the shared volume, and
   replies with the path.
4. The display places the tile over the cursor with `overlay-add`, above the
   scrubber, and swaps it as the cursor moves.

The bridge holds the decoded sheet, so a scrub within one sheet crops from
memory and reads no new file. One sheet covers one hundred tiles, sixteen
minutes at a ten-second interval, so a sheet change during a scrub is rare. When
the mapped tile does not change between two requests, the bridge answers with
the tile it already served, so the overlay does not churn.

## The scan previews, it does not follow

The fine scan moves a cursor and shows the tile there. The video does not
follow the cursor. It plays on at its own position, so the tile previews a
target the picture has not reached, and a scan costs one crop per tile and no
seek. A `select` commits the seek to the cursor. A `back` cancels the scan and
leaves the video where it plays, and leaving the fine stop cancels it the same
way.

Without this the video would chase the cursor. A keyframe seek every hundred
milliseconds would land the picture on the cursor, so the tile would show the
frame already on screen, and the seeking a small machine cannot spare would run
for the length of the scan.

The tile shows only during a scan. At rest the picture is the frame the tile
would show, so the tile would only repeat it. The tile clears on a commit, on a
cancel, when the focus leaves the fine stop, and when the OSD hides. A thin tick
on the bar marks where the video plays while the cursor previews elsewhere.

## The bottom cluster loses some height

The thumbnail sits above the seekbar, so the bottom cluster needs room above the
bar. The scrubber's vertical spacing is a little taller than the text needs, so
this slice tightens it and brings the cluster down in height. The larger strip
rework, its full set of controls and its iconography, is a separate slice after
this one.

## Set aside for this slice

* **Live thumbnails.** A decode of the video for a thumbnail is what this slice
  avoids on a small machine. It is not a fallback here.
* **A sheet the library did not make.** An item with no trickplay in its block
  shows the scrubber with no tile. This slice adds no way to generate one.
* **A mixed interval.** One `trickplayInterval` covers the whole `Play`. A
  playlist whose items came from libraries with different intervals is a later
  concern.
* **The control strip.** Its controls and its icons are the next slice. This
  slice changes only the scrubber's height.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired DualSense. A
`Play` runs a film with a `X.trickplay` directory beside it, and the block
carries the resolved reference.

The drill checks each claim:

* A scan shows a thumbnail on the cursor, cropped from the sprite sheet. The
  precomputed art is on the seekbar.
* The thumbnail tracks the cursor as it moves, and shows the frame at the target
  time. The video holds its own position and does not follow the cursor.
* A `select` commits the seek to the cursor. A `back` cancels the scan and
  leaves the video where it plays.
* The thumbnail shows only during a scan, and it clears on a commit, on a
  cancel, and when the OSD hides.
* A film with no trickplay in its block scrubs with no tile, and draws no broken
  bitmap.

Before the hardware drill, the same path runs on a workstation through
`media-preview`, which decodes the tiles with the real bridge over the real
library. So the thumbnail is seen and tuned locally before the release.
