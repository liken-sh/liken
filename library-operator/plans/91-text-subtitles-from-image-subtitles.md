# 91, Text subtitles from image subtitles

This plan, written on 2026-11-09, records an idea for later: turn the
image subtitle streams inside a video, PGS and VobSub, into text
subtitle files by optical character recognition (OCR). It extends
[plan 60](60-the-subtitle-fact.md), the fact that downloads the
subtitles a video lacks, and does not replace it. Plan 60's text stays
as it is until a design session decides how the two facts divide the
gap.

Status on 2026-11-09: a stub, not designed. Nothing is built. The
work reads every byte of each video in its gap, so it needs a design
session of its own before anyone builds it.

## What a person gains

A screen reader, a search, and the subtitle clues of the marks
estimate cannot read an image subtitle, and a person can style it
only as the disc drew it. A text file made from the image track is in
exact sync with the release on the volume, needs no account, and has
no daily download cap. Plan 60's download then has only the videos
that hold no subtitle in a language at all.

## What `liken` has to build on

- The probe fact (`probe.go`) records each embedded stream in the
  `streams` table, with its codec (`hdmv_pgs_subtitle` or
  `dvd_subtitle` for an image track), its language, its `forced`
  flag, and in `events` the number of subtitle events that mkvmerge's
  statistics tags state.
- Plan 60's gap rule names the videos that lack a text subtitle in a
  household language, from the `streams` rows and the scanner's
  subtitle rows, with the languages from the default
  `MediaPreferences`' `subtitleLanguages`. Its rule says an image
  stream does not count, "because a screen reader and a search cannot
  read it", and that this decision can change.
- The scanner already parses a file named `<video base>.<language>.srt`
  beside a video, with `.hi` or `.forced` before the extension, and
  `subtitlecues.go` reads the quiet stretches of such a file for the
  marks estimate. A file made by OCR would feed both with no change.
- `volumeWriter` (`volumewrite.go`) writes a file beside the media
  with a temporary file and one rename, and never over a file that
  exists.

## The evidence so far

A survey of a home library on 2026-11-09, made for a review of the
media system:

- Of 1,527 film files, 384 had no English text subtitle, embedded or
  beside the file. 346 of those 384 had an English image stream.
- Of 7,997 episode files, 1,565 had no English text subtitle, and
  1,189 of those had an English image stream.
- The catalog held 2,614 English PGS streams and 203 English VobSub
  streams. The 1,740 PGS streams with a count held 3,034,404 events,
  and the largest held 14,292. A PGS track also counts the empty
  events that clear the screen, so it holds fewer captions than events.

No OCR has run on the home library yet, so there is no measured error
rate and no measured cost.

## The compute cost

The subtitle packets of a stream are spread through the whole
container, so a reader must read every byte of the video to collect
them. That is why the probe never extracts a subtitle stream from
inside a video (`subtitlecues.go`). The read is the cost that matters
for a library on network storage: for the gap above it is the whole
of about 1,535 videos. The decode itself is small. No video is decoded,
only the subtitle bitmaps, and OCR runs once for each caption, on the
CPU. Tesseract does not run on OpenVINO, so the GPU that the
appearances fact claims would not help.

The appearances pass already reads every byte of a feature, and its
`ffmpeg` command could write a subtitle stream as a second output in
the same read. But it maps only the first video stream today
(`-map 0:v:0 -an -sn` in `appearances/src/frames.rs`), runs only where
a `Library` turns it on, and covers only titles whose cast has a
headshot. A shared read would tie this fact's gap to that one.

## Licenses and terms

- Tesseract is Apache-2.0, and Debian packages it with its trained
  models for each language, which are Apache-2.0 too.
- FFmpeg's own decoders read both image formats (`pgssub` and
  `dvdsub`) into bitmaps with their times. How the bitmaps reach
  Tesseract is not chosen.
- No outside service is called, so no terms of use apply. The text
  file stays on the volume beside the video, as every other fact's
  output does.

## What a design session must decide

- Whether the fact runs as a heavy fact in `factworkers.go`, and on
  which nodes, given that its cost is the read and not the decode.
- Which languages it reads: the household's `subtitleLanguages`, and
  which Tesseract models the image must then carry.
- Whether it converts every image track in a household language, or
  only the tracks that plan 60's gap names. A forced image track and
  an SDH image track each need a rule.
- How the file names its origin, so a person, Jellyfin, and plan 60
  can tell a file made by OCR from a file a person placed, and whether
  plan 60's gap rule then counts it.
- How italics, colour, and screen position survive, since `.srt`
  carries italics but no position, and whether the output is `.srt`
  or `.ass`.
- How the fact checks its own output: a confidence threshold from
  Tesseract, a check against the `events` count, or a sample a person
  reviews.
- Which fact runs first when a video is in both gaps, so plan 60
  spends no download on a video that OCR would cover.

## What is not decided

The tool chain, the output format, the file name, the order against
plan 60, and the nodes it runs on. Speech recognition for the videos
that hold no subtitle in any form is a separate idea with a larger
cost, and this plan does not cover it.
