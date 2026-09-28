# More kinds

Plan 13. This is a stub for later agents to design, one kind at a time.
The design names music, photos, audiobooks, books, and games. Each kind
needs a new scanner image, a new settings block on `Library`, and a body
layout in the catalog. Each kind also needs screens in the media browser
for that kind, built from the browser's shared primitives, as plan 22
sets out.

## Music

The tags in the files are the authoritative metadata, and the folder
layout is only a hint. `media-operator` plays an album as one timeline
with tracks as chapters, and it reads album art from the files. The
catalog's music body therefore contains the fields that the player
already reads. A music library can have a hundred thousand files. Every
screen stores the whole catalog, so on a one-gigabyte machine, the row
size and row count of a large kind must fit a memory budget.

## Photos and home video

The metadata comes from EXIF and from XMP files. The library is
organized by date, place, and person. No metadata provider exists to
enrich from. A photo library has about a hundred thousand small items.
The proof of concept measured this scale at 105,000 rows. The idle
screen is the first consumer of photos: a slideshow is a `Play` of a
folder.

## Audiobooks

Audiobooks play on speakers like music. The structure is author, then
book, then chapters. Watch state must record the position inside a long
file.

## Books

The catalog records books like the other kinds, with covers and an OPF
file, such as Calibre's metadata.opf. No screen shows books, because
people read a book on a phone or a tablet. A reader app is one more
consumer of the same catalog. This is why the library layer runs below
playback.

## Games

The design names games so that a later plan can add them. An emulator
is one more consumer of the catalog, like a player. A game library's
structure is platform, then title.
