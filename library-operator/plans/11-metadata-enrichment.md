# Metadata enrichment

Plan 11. A stub for a later agent to shape. It builds the third
responsibility from the design: fetching what the volume does not hold,
from named providers, into the same sidecar files the scanners read.

## The problem

Jellyfin enriches the lab's volume today. Its `SaveLocalMetadata`
setting writes `movie.nfo`, `tvshow.nfo`, the artwork, and the
`.trickplay` tiles beside the files, from TMDb, OMDb, and Fanart. The
`*arr` tools' own metadata writers are off. That works, and it makes the
catalog depend on a program outside the cluster. About a fifth of the
lab's movies had no sidecar when the design was made, and a scanner can
only report that.

## The shape

- A `MetadataSource` resource, with the same discriminator-and-block
  shape as `Library`: one provider per block, with its `Secret`
  reference for a key and its settings. The provider fixes which kinds
  it serves. A `Library`'s ordered `sources` list, in the schema since
  plan 02, picks and orders them.
- An enricher per provider, in its own pod, because it holds keys and is
  the only part of the operator that connects to the internet. Its
  network policy allows that and no other pod's does.
- It writes sidecars in the ecosystem's formats and nothing of its own:
  `.nfo`, art files, and WebVTT thumbnail sprites, which a text file and
  a JPEG describe and which web players and Roku read. The scanner
  detects the new files and updates the catalog. The enricher never
  writes the catalog.
- Two writers of `.nfo` in one folder is a hazard. The day an enricher
  turns on for a library, Jellyfin's saver turns off for it, and the
  plan states how that handover is checked.
- The ownership and mode of files another tool wrote matter. On the
  lab's volume the art was root-owned with mode 644, so the enricher's
  user must be able to overwrite what Jellyfin wrote.

## What is not decided

Whether the enricher and the scanner share code, since both parse the
same formats. Whether to identify unidentified folders by a name search,
and how a person confirms a guess. How rate limits and provider outages
appear in `Library` status.
