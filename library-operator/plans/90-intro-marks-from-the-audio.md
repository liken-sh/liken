# 90, Intro marks from the audio of a season

This plan, written on 2026-11-09, records an idea for later: find the
intro of a series' episodes from the audio that the episodes of a
season share, for the seasons that neither intro database covers. It
carries forward the "Detection in the media" item of the Later section
of [plan 62](completed/62-intro-and-credits-marks.md).

Status on 2026-11-09: a stub, not designed. Nothing is built. The
work decodes audio from every episode in its gap, so it needs a design
session of its own before anyone builds it.

Status on 2026-11-17: nothing is built.
[Plan 96](completed/96-end-titles-from-the-picture.md) found the end
titles from the picture and left the main titles to this plan, so
[A second method, the picture](#a-second-method-the-picture) records
that method beside the audio, and the design session chooses between
them.

## What a person gains

The display offers a skip over the intro only when the `marks` table
holds an intro span for the file. An episode with no span from
TheIntroDB, IntroDB, or the estimate shows no skip, so a person who
binges a season watches the same title sequence on each episode. A
span found from the audio gives those episodes the same skip that the
covered series have.

## What `liken` has to build on

- The `marks` fact (`theintrodb.go`, `introdb.go`) writes the
  databases' spans to `.liken/marks.yaml` and the `marks` table, with
  the database's name in `source`.
- `markestimate.go` writes the one answer that every reader uses for
  a video's intro, as a row with the source `estimate`, from every
  clue that the catalog holds. Its inferred tier already takes the
  span that the other episodes of the same season have, and the quiet
  stretches that `subtitlecues.go` reads from a text subtitle file
  beside the video. The browser reads only the estimate's row, so a
  span found from the audio reaches a player only as one more clue
  for that estimate.
- `factworkers.go` runs the heavy facts, trickplay, appearances, and
  titles, as Indexed Jobs of their own, one pod for each video, so a
  decode never holds the library Job's walks and webhook rescans.
  Trickplay and appearances decode a whole video, and titles decodes
  only the end of one.
- The appearances tool splits its work into a pass for each video
  (`appearances detect`, which writes a record under `.liken/`) and a
  pass for each title folder (`appearances match`). A fingerprint for
  each episode and one comparison for each season would have the same
  shape.

## The evidence so far

- On a home library on 2026-11-09, 1,615 episode files in 114 series
  held no intro span from any source, the estimate included. The same
  catalog held 4,940 intro spans from IntroDB, 4,596 from TheIntroDB,
  and 959 estimates.
- The method is in use elsewhere: the `intro-skipper` plugin for
  Jellyfin fingerprints the audio of a season's episodes with
  Chromaprint and takes the run that they share as the intro.
- Plan 62 recorded the limits: the result depends on the rip, and a
  cold open or a recap can move the span.

No fingerprint has run on the home library yet, so there is no
measured hit rate and no measured cost.

## The compute cost

A fingerprint needs the decoded audio of the part of each episode
where an intro can be, which the media review of 2026-11-09 put at the
first 10 minutes. For the gap above that is about 16,000 minutes of
audio, decoded on the CPU, because FFmpeg decodes audio on the CPU on
every machine. The read is only the first part of each file, not the
whole file.

The appearances pass does not bound this cost and cannot share it as
it is. Its `ffmpeg` command maps only the first video stream and drops
the audio (`-map 0:v:0 -an` in `appearances/src/frames.rs`). It also
runs only where a `Library` turns the fact on, and only for titles whose
cast has a headshot, so its gap is not this gap. Trickplay decodes
every video, but it decodes the whole file for pictures, and a shared
pass would tie the two facts' gaps together.

## A second method, the picture

[Plan 96](completed/96-end-titles-from-the-picture.md) built the
`titles` tool, which reads the text on screen at the end of each video
and places the end titles with a model that each cluster fits on its
own library. Its design says that the same job can read the start of a
file for the main titles later, and that nothing in the reading or the
model's inputs is tied to the end. It also records a limit: a title
sequence shows few of the names that the catalog holds, and the model
places the end titles by those names.

No reading of the start of a video has run, so there is no measured
hit rate for the main titles from the picture. The worker, the image,
the ledger, and the placing `Job` already exist, so this method adds
no new tool, where the audio method adds Chromaprint.

## Licenses and terms

- Chromaprint's library is LGPL-2.1-or-later, and Debian packages it.
  The design must confirm the license of the build that the image would
  carry, because a Chromaprint built against FFTW3 is under the GPL.
- FFmpeg has a `chromaprint` muxer when it is built with the library.
  Whether the repository's `ffmpeg` base image has it is not checked.
- The fingerprints are compared only with each other, inside one
  season. No fingerprint goes to AcoustID, so AcoustID's API terms do
  not apply.

## What a design session must decide

- Which method finds the intro: the audio that a season's episodes
  share, the picture read by the `titles` tool, or both as clues of
  one estimate.
- Where the work runs: a new heavy fact in `factworkers.go`, or a step
  of an existing pass.
- How much of each episode to fingerprint, and whether the end of each
  episode is fingerprinted too, for the credits.
- How the comparison runs for each season when each worker pod sees
  one video, and where the fingerprints are kept between the two steps.
- What a found span is: an exact clue, an inferred clue of
  `markestimate.go`, or a candidate with its own `source`. The browser
  reads only the estimate's row, so a candidate with its own `source`
  reaches a player only when the estimate reads it as a clue. Plan 62
  proposed a confidence value and a confirmation by a person before the
  player offers the skip.
- How the fact handles a season with a cold open, a recap before the
  intro, a season with no intro, and a season of one episode.
- How a span found in one season reaches the seasons that the
  databases cover only in part.

## What is not decided

The tool, the window, the record format, the place in the marks
precedence, and whether this fact runs on a GPU node or on any node.
None of these is chosen here.
