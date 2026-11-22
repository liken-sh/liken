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

Status on 2026-11-22: still a stub. An experiment on a laptop on
2026-11-21 measured two OCR engines on image cues from a home library,
and "The experiment of 2026-11-21" below records it. It ran for the
design of [root plan 95](../../plans/completed/95-subtitles-on-one-row-in-one-style.md),
which draws an image track on the row and at the size of a text track
and leaves the disc's font. Text made from the image track would take
the whole house style. Since
[plan 97](completed/97-the-probe-measures-image-subtitle-tracks.md)
the probe reads a window of each image subtitle stream and decodes its
bitmaps in Go, so a part of this plan's reader exists. Two sentences
below are older than both: "No OCR has run on the home library yet",
and the statement that the probe never extracts a subtitle stream from
inside a video.

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

## The experiment of 2026-11-21

The sample was 24 files of a home library, 18 with a PGS stream and 6
with a VobSub stream, each with a text subtitle track in the same
language as a reference. A 600-second window of each image stream gave
4,212 cues. The experiment paired the cues with the reference's cues
into 3,574 groups. For 2,340 groups the reference was the same
transcription as the picture. For the other 1,234 it was not: the
reference was another transcription, text in all capitals, or the
output of an older OCR. The rates below are for the 2,340 groups, as
the share of wrong characters and the share of groups with no wrong
character.

The engines were Tesseract 5.5.4 with its `tessdata_best` model, and
the PP-OCRv6 recognition models as ONNX files on OpenVINO 2026.4.1, on
the laptop's CPU. Each got one line of a cue at a time, drawn as white
text on black, which was the best of the preparations that were tried.

| Engine | PGS, 2,057 groups | VobSub, 283 groups |
|---|---|---|
| PP-OCRv6 small | 0.62% wrong, 93.6% exact | 3.74% wrong, 87.3% exact |
| Tesseract best | 0.97% wrong, 80.0% exact | 4.32% wrong, 77.4% exact |
| Tesseract best, then a pass that corrects `l`, `I`, and `1` | 0.47% wrong, 94.9% exact | 3.83% wrong, 86.6% exact |

- **Tesseract confuses `I`.** It wrote `|` for `I` 347 times in the
  68,638 reference characters of the PGS groups, which is most of its
  errors. PP-OCRv6 small made 5 errors of that class.
- **A person's check agreed.** Of 60 PGS groups and 60 VobSub groups
  that a person compared with the picture, PP-OCRv6 small had 58 and
  59 right.
- **Speed.** For each cue, on the CPU, as the mean of 150 cues of 4
  tracks: PP-OCRv6 tiny 12 ms, Tesseract fast 15 ms, PP-OCRv6 small 47
  ms, Tesseract best 49 ms, and PP-OCRv6 medium 209 ms. The medium
  model was no more accurate than the small one. At 47 ms, a track of
  1,500 captions takes about 70 seconds. On the laptop's Intel GPU the
  small model was about 14 times slower than on its CPU.
- **Confidence marks the errors.** In the tenth of the groups where
  PP-OCRv6 small was least confident, 33% had an error, against 4% of
  the rest.
- **Italics can be read from the picture.** A measure of the slant of
  each line agreed with the reference's `<i>` marks on 96.4% of 3,155
  lines. It found 85% of the italic lines, and 72% of the lines it
  called italic were italic.
- **Some discs place cues by speaker.** 371 of the 3,154 PGS cues,
  11.8%, were more than a tenth of the canvas width off centre, in 5 of
  the 18 PGS tracks, and 3% of the cues were at the top of the frame.
  No VobSub cue was off centre. A text file made from such a track
  would need each cue's side to keep that.
- **The text tracks beside these files were often worse than the
  picture.** A third of the groups had a reference that was not the
  disc's transcription. So a text track made by OCR can be better than
  the text track that a file already has, and the gap rule of plan 60
  cannot treat every text track as good.
- **The model's licence is not settled.** The model cards of PP-OCRv6
  state Apache-2.0, and the repositories that publish the model files
  had no licence file on 2026-11-21.

The experiment did not measure languages other than English, a whole
track, or a cluster node's CPU.

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
