# The organizer

Plan 12. A stub for a later agent to shape. It builds the fourth
responsibility from the design: renaming and moving files to a library's
naming convention.

## The problem

Radarr and Sonarr organize the lab's volume today. They rename and move
files into `Title [Year]` folders, season folders, and episode names, by
conventions set in their configuration. The scanners read those
conventions from the `Library` settings blocks. A cluster that replaces
those tools, or that receives files another way, has nothing that puts a
file where the convention says it goes.

## The shape

- The organizer is a loop of its own, and the only loop that moves or
  renames anything. The scanner reads, the enricher adds sidecars beside
  files, and the organizer moves folders.
- It takes the convention from the same settings block the scanner
  parses by, so the two agree on what a name means.
- A move is announced to the scanner through the same webhook path an
  import uses, so the catalog updates the row rather than losing one
  title and finding another.
- It is opt-in per `Library`, and it stays off for a library that
  another tool organizes, because two organizers with two conventions
  would move files back and forth.

## What is not decided

Whether a move is proposed and confirmed or applied on sight. How a move
that fails halfway is reported and finished. Whether the organizer also
owns an inbox: a folder where new files land before they have a place.