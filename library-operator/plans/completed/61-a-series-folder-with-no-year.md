# 61, A series folder with no year

Built on 2026-09-11. The identity ladder gained a rung that reads the
episode titles off a series folder's file names and matches them
against each candidate's season on TMDb. A series folder named with the
title alone now identifies without a sidecar.

## The problem

A namer writes a series folder as the title alone, with no year. The
ladder in [plan 29](29-identification.md) searches TMDb on the title,
and a title alone matches every show that ever carried it. The runtime
rung parts them only where the probe measured a length and the lengths
differ, and an episode's length rarely does. So a bare series folder
identified today only through the `tvshow.nfo` another tool wrote
beside it, and this operator's goal is that nothing else writes beside
the media.

## The design

**The clue.** An episode file is named with its marker and its title,
as in `<series> - S01E02 - <title>.mkv`. The text after the marker up
to the next ` - `, with every trailing bracketed or parenthesized tag
cut off, is the clue. A file with no marker or nothing after it gives
none. A double episode takes its first number, because the title after
the marker names that one.

**The folder's clues.** The rung takes the clues of the first season
the folder holds, in episode order, capped at twelve. Season zero is
the specials, whose names are the weakest evidence a show has, so it
counts as the first season only where the folder holds nothing else.

**The rung.** It runs for a series when several candidates remain after
the country rung and before the runtime rung. For each candidate it
fetches the season the clues name, once, and counts the clues whose
episode number and normalized name both match. A candidate is kept
with at least two matches and at least half of the clues. One survivor
is written with the reason `title and episode names`. None or several
fall through to the runtime rung and then to the candidate list, as
before.

**Two shows with one title.** Two series that share a title would share
a folder, so the case has not come up. When the rung keeps both or
neither, the answer is the candidate list and the ledger that movies
use today.

**What was set aside.** Stripping release tokens out of a dotted scene
name such as `Show.S01E02.Episode.Name.1080p.mkv`. Such a clue matches
nothing and is wasted, never wrong, and a namer that writes that form
also writes no title the rung could read.

## How the work is proved

Table tests cover the clue parser, the rung with two candidates where
the episode names pick one, where neither matches, where both match,
where fewer than two match, and a movie kind, which never asks for a
season. One test walks the whole identity fact over a bare series
folder against a fake TMDb and reads the id out of the sidecar.
