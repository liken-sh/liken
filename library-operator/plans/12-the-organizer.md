# The organizer

Plan 12. This is a stub for a later agent to design. It builds the fourth
responsibility from the design: renaming and moving files to a library's
naming convention.

## The problem

Radarr and Sonarr organize the lab's volume today. They rename and move
files into `Title [Year]` folders, season folders, and episode names, by
conventions set in their configuration. The scanners read those
conventions from the `Library` settings blocks. A cluster that replaces
those tools, or that receives files another way, has nothing that puts a
file where the convention says it goes.

## Design

- The organizer is a separate loop, and it is the only loop that moves or
  renames anything. The scanner only reads files. The enricher only adds
  metadata files beside the media. The organizer moves folders.
- It reads the convention from the same settings block that the scanner
  uses to parse names, so the two agree on what a name means.
- The organizer reports a move to the scanner through the same webhook
  path that an import uses. The catalog then updates the existing row. It
  does not delete one title and add another.
- It is opt-in per `Library`, and it stays off for a library that
  another tool organizes, because two organizers with two conventions
  would move files back and forth.

## What is not decided

Whether the organizer proposes a move for confirmation, or applies it at
once. How the organizer reports and completes a move that fails halfway.
Whether the organizer also owns an inbox: a folder where new files
arrive before they have a place in the library.
