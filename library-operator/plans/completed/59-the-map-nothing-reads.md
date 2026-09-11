# 59, The map nothing reads

Built on 2026-09-11. The trickplay fact no longer writes a WebVTT map
beside its sheets, and it removes the maps earlier runs wrote. The
tile directory it leaves is byte for byte the one Jellyfin's own
extraction leaves.

## The problem

[Plan 30](30-facts-art-and-contributors.md) wrote `tiles.vtt` beside
the sheets, on the belief that a screen would read the map. No screen
does. The media-operator's bridge reads the layout folder's name for
the tile width and the grid, and the ten-second interval is Jellyfin's
own, so the bridge maps a time to a sheet and a cell with no file. The
media browser never opens the map either.

The map has a cost on a volume Jellyfin shares. Jellyfin imports a tile
directory it did not make and records the number of files in the
folder as the thumbnail count. Read against Jellyfin 10.11's source on
2026-09-11:

* jellyfin-web never reads that count. Its scrub bubble divides the
  time by the interval and the grid, as the bridge does, so a wrong
  count changes nothing a browser shows.
* The count feeds Jellyfin's HLS image playlist alone, which some
  clients read for their scrub previews. There a count of eight makes
  a playlist of one sheet with eight cells, and the previews stop
  after eighty seconds.

Our map made the count wrong by one. Without it the count is the sheet
count, which is still not the thumbnail count. That is an upstream
matter: the import could compute the count from the runtime and the
interval, as the extraction does. Whether to file it is open.

## The design

**No map.** The fact stages the sheets alone and lands them with one
rename, as before.

**The sweep.** The write package gains one narrow door: it removes a
file only when the file is named `tiles.vtt` inside a layout folder
inside a `.trickplay` directory, and it refuses every other name. At
the start of each run the fact lists the videos whose catalog row
names a tile directory, and removes the map in each one that has it.
The sweep is one `stat` per tiled video per run and no decode, so the
first run after this change clears every map with no extraction.

**Beside Jellyfin.** With the map gone, a directory this fact makes and
a directory Jellyfin makes are the same sheets under the same names.
The fact treats a directory it finds as done, whoever made it, and the
scanner records it. So the race for a new title has no loser: the
first extractor's sheets serve both.

## How the work is proved

Table tests cover the door's path rule and one real removal. A run
over a tiled video with a stale map removes the map and leaves the
sheets; a run over one with no map changes nothing; the gap run writes
no map. The drill on `liken-1` reads the sweep's count out of the Job
log on the first run after the roll, and reads zero on the second.
