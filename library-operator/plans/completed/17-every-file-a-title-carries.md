# Every file a title carries

Plan 17. The rest of the folder. At the end of this plan the catalog
holds every file a title carries, each one classified by what it is, and
the media browser draws a title's art, its subtitles, and its extras
from the catalog alone.

## The problem

A title folder holds more than its video. Jellyfin and the `*arr` tools
write a `movie.nfo` or a `tvshow.nfo`, a `folder.jpg`, a backdrop, a
logo, an episode thumbnail, a `.trickplay` directory of tiles, and the
subtitle files a person added. A movie folder often holds an `Extras`
or a `Trailers` folder beside the feature.

The walk reads the video files and nothing else. An item row carries the
path of its poster and the list of the three art files
[plan 04](completed/04-scanners-for-movies-and-series.md) looks for, and
a file row carries the path of its trickplay directory. Everything else
on the volume is invisible to the catalog.

The media browser is what needs them. A title page draws a poster, a
backdrop, and a logo; a playback screen offers the subtitle tracks; a
title with extras offers them under the feature. Each of those is a file
on the volume, and the browser reads the catalog and not the volume, so
a file the catalog does not hold is a file the browser cannot offer.

## Every file is a row

Every file a title folder holds becomes a `files` row, and not the video
files alone. A file row already carries the path, the library, the
container, the size, and the item links, and the prune already sweeps a
file row by its path. The new rows are ordinary file rows, so the
mark-and-sweep needs no change. The walk marks every path it read with
the walk's epoch, and the prune deletes the file rows this epoch did not
mark. A poster a person deletes leaves the catalog on the
next walk, exactly as a video does.

The walk reads three places for one title:

* the title folder itself, for the sidecar, the art, the subtitles, and
  the video;
* a season folder under a series, for the same;
* an extras folder under a title folder, for the trailers and the
  featurettes, by the fixed set of names Jellyfin uses: `extras`,
  `featurettes`, `trailers`, `behind the scenes`, `deleted scenes`,
  `interviews`, `scenes`, `shorts`, `clips`, and `other`.

The walk descends no further. A `.trickplay` directory holds one tile
per few seconds of video, and a large library holds millions of them, so
the directory is one row and its tiles are none. The walk skips the junk
a storage appliance leaves: a name that starts with a dot, `Thumbs.db`,
and `desktop.ini`.

Every file links to the item whose folder holds it, through the
`file_items` table that already exists. A file in a movies title folder
or its extras folder links to the movie. A file directly in a series
folder links to the series. A file in a season folder links to the
episode whose own name it starts with, and to the series where it
matches no episode, which is where a season poster lands.

## What each file is

The `files` table gains four columns: `type`, `role`, `language`, and
`modified`.

`type` is the standard category, one word from a closed set. The set is
closed so that a browser can switch on it, and a re-walk classifies the
same file the same way.

| `type` | What it holds |
| --- | --- |
| `video` | the feature, an episode, a trailer, or an extra |
| `audio` | a theme song, and a music track when music arrives |
| `subtitle` | a subtitle track beside the video |
| `image` | a poster, a backdrop, a logo, or a thumbnail |
| `metadata` | an `.nfo` sidecar |
| `trickplay` | a directory of thumbnail tiles |
| `other` | a file in none of the categories above |

`role` says which one of its kind the file is. The vocabulary is
Jellyfin's and Kodi's, because that is what wrote the files.

| `type` | `role` |
| --- | --- |
| `video` | `primary`, `trailer`, `extra`, `theme`, `sample` |
| `audio` | `theme`, `track` |
| `subtitle` | `full`, `forced`, `sdh` |
| `image` | `poster`, `backdrop`, `logo`, `banner`, `thumb`, `disc`, `clearart`, `still` |
| `metadata` | `movie`, `tvshow`, `episode`, `season`, `collection` |
| `trickplay` | `tiles` |
| `other` | empty |

Two roles are read from the place and not the name. An image in a
season folder whose name states none of the image words is the `still`
beside an episode. An image anywhere else whose name states none of them
carries no role, because `screenshot.jpg` is one of the eight roles only
if a person says which, and the scanner invents nothing.

`language` is the language a subtitle or an audio file carries, read off
the file name in the form the tools write, `The Matrix (1999).en.srt` or
`The Matrix (1999).en.forced.srt`. It is a two-letter or three-letter
tag as the name gave it, with no translation between the two, and it is
empty where the name carries none.

`modified` is the time the file was last written, in Unix seconds. The
walk already reads a file's size, and the same read carries its time, so
this column costs nothing and answers "what changed" without a second
pass over the volume.

The columns already there keep their meanings. `container` is the
extension, so a subtitle's format reads there. `size_bytes` is the size
of every file, whatever its type. `width` and `height` stay the video's
frame; the walk does not open an image to measure it, and
[richer file facts](open-problems/richer-file-facts.md) covers the facts
that need a file opened.

The video row's `trickplay` column stays. It answers "the tiles for this
file" in one read, where the row of type `trickplay` puts the same
directory in the list of everything the title carries. Both come from
the one walk that found the directory, so they cannot disagree.

## What was considered and set aside

**A `body` column on a file row.** The item tables carry a JSON body for
the shape their kind does not share, and a file row could do the same.
Today it would be empty for every type: the format is the container, the
language and the flags are their own columns, and everything else needs
a file opened. Corrosion adds a column to a live table without a
migration, so the day a fact needs a home it gets a column with a name.

**One row per trickplay tile.** The tiles are real files, and the type
vocabulary has a word for them. A single film's tiles run to hundreds of
files and a library's to millions, all of them derived, none of them
addressable on their own. The directory is the unit the player asks for.

**Skipping the files in no category.** A `.txt`, an `.sfv`, or a `.url`
beside a movie is noise a browser will never draw. It is also on the
volume, and a catalog that holds every file but those is a catalog a
person cannot trust to answer "what is in this folder". They are typed
`other`, and a query that needs none of them excludes that one type.

## The proof

The unit tests read a folder tree of every case: a movie with a sidecar,
art, two subtitles with languages and a forced flag, a trickplay
directory, and an `Extras` folder; a series with a `tvshow.nfo`, a
season poster, an episode with its own `.nfo`, thumbnail, and subtitle;
and a folder of junk that yields no rows. The prune tests prove a
deleted poster leaves the catalog on the next walk, and that a webhook
rescan of one title folder prunes that title's files and no other's.

The drill runs on `liken-1` against the real volume. Record the file
count before and after, the walk's duration, and a `SELECT type, count(*)`
over the catalog, so the plan carries the shape of a real library.
