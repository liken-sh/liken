# The organizer

Plan 11. A stub, at low fidelity, for a later agent to shape. It is
the one responsibility that writes to the structure of the share:
renaming and moving files to a library's naming convention.

## The problem

Radarr and Sonarr organize the lab's share today: they rename and
move files into `Title [Year]` folders, season folders, and episode
names, by conventions set in their configuration. The scanners read
those conventions from the `Library` kind blocks. A cluster that
replaces those tools, or that receives files some other way, has
nothing that puts a file where the convention says it goes.

## The shape

- The organizer is a loop of its own, and it is the only loop that
  moves or renames anything. The scanner reads, the enricher adds
  sidecars beside files, and the organizer moves folders.
- It takes the convention from the same kind block the scanner parses
  by, so the two can never disagree about what a name means.
- A move is announced to the scanner through the same webhook path an
  import uses, so the catalog updates the row rather than losing one
  title and finding another.
- It is opt-in per `Library`, and it is never on for a library that
  another tool already organizes, because two organizers with two
  conventions would move files back and forth.

## What is not decided

Whether a move is proposed and confirmed or applied on sight. How a
move that fails halfway is reported and finished. Whether the
organizer also owns the inbox: a folder where new files land before
they have a place.
