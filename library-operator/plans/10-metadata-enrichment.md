# Metadata enrichment

Plan 10. A stub, at low fidelity, for a later agent to shape. It
adds the third responsibility from the design: fetching what the
share does not yet hold, from named providers, into the same sidecar
files the scanners read.

## The problem

Today Jellyfin enriches the share: its `SaveLocalMetadata` setting
writes `movie.nfo`, `tvshow.nfo`, the artwork, and the `.trickplay`
tiles beside the files, from TMDb, OMDb, and Fanart. The `*arr`
tools' own metadata writers are all off. That works, and it means
the cluster depends on a program outside it for its catalog to be
rich. About a fifth of the lab's films had no sidecar when the design
was made, and a scanner can only report that.

## The shape

- A `MetadataSource` resource, with the same discriminator-and-block
  shape as `Library`: one provider per block, with its `Secret`
  reference for a key and its settings. The provider fixes which
  kinds it serves; a `Library`'s ordered `sources` list, present in
  the schema since plan 02, picks and orders them.
- An enricher per provider, in its own pod, because it holds keys and
  is the only part of the operator that reaches the internet. Its
  network policy allows that and nothing else's does.
- It writes sidecars in the ecosystem's formats and nothing of its
  own: `.nfo`, art files, and WebVTT thumbnail sprites, which are the
  one thumbnail format that a text file and a JPEG describe and that
  web players and Roku read. The scanner notices the new files and
  updates the catalog; the enricher never writes the catalog.
- Two writers of `.nfo` in one folder is a hazard. The day an enricher
  turns on for a library, Jellyfin's saver turns off for it, and the
  plan states how that handover is checked.
- The ownership and mode of files written by another tool matter: on
  the lab's share the art was root-owned and mode 644, so the
  enricher's user has to be able to overwrite what Jellyfin wrote.

## What is not decided

Whether the enricher and the scanner share code, since both parse
the same formats. Whether to fill the gap for unidentified folders
by name search, and how a person confirms a guess. How rate limits
and provider outages are surfaced in `Library` status.
