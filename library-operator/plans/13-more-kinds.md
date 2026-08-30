# More kinds

Plan 13. A stub for later agents to shape, one kind at a time. The
design names music, photos, audiobooks, books, and games. Each is a new
scanner image, a new settings block on `Library`, a body shape in the
catalog, and views in the media browser that the media browser is given
rather than built with.

## Music

Tags in the files are the truth, and the folder layout is a hint.
`media-operator` plays an album as one timeline with tracks as chapters
and reads album art from the files, so the catalog's music body contains
what the player already reads. Music libraries reach a hundred thousand
files, which makes the catalog's size on every screen a budget question;
see [every node stores every
table](open-problems/every-node-stores-every-table.md).

## Photos and home video

EXIF and XMP sidecars are the metadata. Dates, places, and people are
the structure. There is no provider to enrich from. A photo library is a
hundred thousand small items, the scale the proof of concept measured at
105,000 rows. The idle screen is its first consumer: a slideshow is a
`Play` of a folder.

## Audiobooks

Audiobooks play on speakers like music. The structure is author, then
book, then chapters, and watch state has to record the position inside a
long file.

## Books

Books are cataloged like the rest, with covers and an OPF or Calibre
sidecar, and have no consumer on a screen: a book is read on a phone or
a tablet. A reader is one more consumer of the same catalog, which is
why the library layer runs below playback.

## Games

Games are named so the design leaves room. An emulator is one more
consumer, like a player, and a game library's structure is platform,
then title.