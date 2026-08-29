# More kinds

Plan 12. A stub, at low fidelity, for later agents to shape, one kind
at a time. The design names music, photos, audiobooks, books, and
games, and each is a new scanner image, a new kind block on
`Library`, and a new set of views in the browser.

## Music

Tags in the files are the truth, and the folder layout is a hint.
`media-operator` already plays an album as one timeline with tracks
as chapters and reads album art from the files, so the catalog's
music body carries what the player already understands. Music
libraries reach a hundred thousand files, which is the first kind
that makes the catalog's size on every screen a budget question; see
[every node holds every table](open-problems/every-node-holds-every-table.md).

## Photos and home video

EXIF and XMP sidecars are the metadata, and dates, places, and people
are the natural structure. There is no provider to enrich from. A
photo library is a hundred thousand small items, which is the scale
the catalog's proof of concept measured at 105,000 rows, and the idle
screen is its first consumer: a slideshow is a `Play` of a folder.

## Audiobooks

Ordinary on the playback side, since they play on speakers. The
catalog structure is author, then book, then chapters, and the
position within a long file is what watch state has to hold.

## Books

Cataloged like the rest, with covers and an OPF or Calibre sidecar,
and with no consumer on a screen: a book is read on a phone or a
tablet. Books are the kind that proves the library sits below
playback, because a reader is one more consumer of the same catalog.

## Games

Named so the design leaves room. An emulator is one more consumer,
like a player, and a game library's structure is platform, then
title.

## What every new kind adds

A scanner image, a typed block in the `Library` schema, a body shape
in the catalog, and views in the browser that the browser is told
about rather than knows. Each one is its own plan.
