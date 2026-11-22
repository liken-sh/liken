# 97, The probe measures image subtitle tracks

Proposed and built on 2026-11-21. The operator's tests pass.

[Root plan 95](../../../plans/completed/95-subtitles-on-one-row-in-one-style.md)
states the rule that this plan serves: the last line of every subtitle
ends on one screen row at one text size.

## The problem

An image subtitle track, PGS or VobSub, is a set of pictures. Each
disc's author chose a canvas, a text size, and a row. On 24 tracks
sampled from a home cluster on 2026-11-21, the text of a bottom cue
ended between row 918 and row 1026 of a 1080-row canvas, and one line
of text was 51 to 73 rows tall. mpv draws what the disc states, so the
player needs three numbers for each track before it can move the cue
to the house row and shrink it to the house size. mpv cannot report
them, because it receives the pictures one cue at a time during
playback.

## The design

The probe fact measures each image subtitle stream once and records
the result in the ledger. Every reader after that uses the recorded
numbers.

### The three numbers

The numbers are in rows of the track's own canvas.

- **`canvasHeight`**: the height of the canvas. A PGS stream states it
  in each presentation composition segment, and the probe takes the
  one of the stream's first cue. A VobSub stream states it in the
  `size:` line of its extradata. FFmpeg reads that line into the
  stream's height, so the probe takes it from the header read. A
  VobSub stream with no height gets no numbers.
- **`lineHeight`**: the height of one line of text that has both
  ascenders and descenders.
- **`inkBottom`**: the row just below the last row of text of a usual
  bottom cue.

The probe computes them from the decoded bitmaps of a sample of cues.
These definitions come from the 24 sampled tracks, and each one is
written so that a second implementation gives the same numbers.

- A **bitmap** is one picture as mpv receives it. For PGS that is one
  object of a composition, at the position the composition gives it,
  with its transparent margins. For VobSub it is the display area
  cropped to the smallest box of pixels whose alpha is not zero,
  because FFmpeg's decoder crops it that way before mpv sees it.
- A **cue** is one bitmap from the time it appears until it leaves the
  screen. A PGS stream can show the same objects again in a later
  composition, for a fade or at a point where a player can start to
  read. That is one cue, measured with its most opaque palette.
- An **ink row** is a row of a bitmap with at least one pixel of alpha
  128 or more. A VobSub pixel has a 4-bit alpha, and 8 or more counts.
- A **line** is a run of consecutive ink rows, and its height is the
  number of rows in the run.
- A **lower cue** is a bitmap whose top row is at or below half the
  canvas height. mpv moves only those bitmaps with `sub-pos`.
- `lineHeight` is the 90th percentile, by nearest rank, of the heights
  of every line of every bitmap that was read. Sort the heights and
  take element `ceil(0.9 × n) − 1`.
- `inkBottom` is the median, over the lower cues whose last line is at
  least `lineHeight − 1` rows tall, of the bitmap's top row plus the
  row just below its last line. Sort the values and take element
  `n / 2`. When no lower cue has such a line, every lower cue counts.

`inkBottom` counts only full-height last lines because about a third
of last lines have no descender and end 11 to 14 rows higher. With
that restriction the median was within 1 row of its 600-second value
from any 10 consecutive lower cues, on all 24 tracks. Without it the
median needed about 80 cues.

### The read

After the header read that the probe makes today, a video file with
an image subtitle stream gets a second read:
`ffprobe -select_streams s -read_intervals <start>%+300 -show_packets
-show_data`. One read returns the packets of every subtitle stream of
the file. The probe decodes the packets of the image streams as they
arrive and ignores the others.

- The first window starts at a quarter of the file's duration, so it
  starts after the opening titles. Each window is 300 seconds long.
- The probe stops `ffprobe` when every image stream has 20 lower cues.
- A stream that has fewer than 10 lower cues after the first window
  gets a second window, which starts at half of the duration, and the
  cues of both windows count together. The windows of a file shorter
  than 1,200 seconds overlap, and a packet of the first window does
  not count again in the second.
- A stream that still has fewer than 10 lower cues gets no numbers.
- A file with no duration gets one window, at its start.
- Each window has a limit of one minute, the limit that the header
  read has. When the limit ends a window, the cues that arrived count.

`ffprobe` with only subtitle streams selected stops at the first
subtitle packet after the window. In a long stretch with no dialogue
that packet can be minutes of file away, so the probe's own limit and
its count of cues bound the read, and `ffprobe`'s interval does not.

The probe decodes the packets in Go. A PGS packet is a set of
segments with a palette and run-length-coded objects. A VobSub packet
is one sub-picture unit with two interlaced run-length-coded fields.
The probe image already contains `ffprobe`, and a decoder in Go needs
no new program in the image.

### The record

A stream's entry in `.liken/probe.yaml` gains one optional block:

    image:
      canvasHeight: 1080
      lineHeight: 61
      inkBottom: 918

The file's record gains `measured: true`, which every record has that
a probe with this measurement wrote. A record without it that
contains an image subtitle stream opens the probe gap again, the way
a record without `counted: true` does. A stream that was read and
gave too few cues has no `image` block in a record with
`measured: true`, so the probe does not read it again.

### The catalog and the play request

The `streams` table gains three nullable integer columns at its end:
`image_canvas_height`, `image_line_height`, and `image_ink_bottom`. A
null means the stream is not measured.

The media browser adds an `image` object to the track's entry in
`items[].presentation.subtitles` when the three columns have values:

    {"stream": 3, "events": 1382,
     "image": {"canvasHeight": 1080, "lineHeight": 61, "inkBottom": 918}}

The operator copies the object onto the `Play` unread, as it copies
`events`. `media-operator` plan 46 defines the field of the `Play` and
what the player does with it.

## What a read costs

A `Job` on a home cluster on 2026-11-21 read a 300-second window of
every subtitle stream of 10 files from a NAS, each window one that no
earlier read had cached.

| Files | Bitrate | Bytes read | Time |
|---|---|---|---|
| 4 PGS films, 4 to 10 GB | 4.8 to 8.6 Mbit/s | 225 to 319 MB | 2.0 to 3.2 s |
| 3 PGS episodes | 2.6 to 4.9 Mbit/s | 97 to 258 MB | 1.1 to 2.4 s |
| 3 VobSub files | 2.2 to 5.4 Mbit/s | 95 to 183 MB | 0.9 to 1.7 s |

Every read ran at 92 to 111 MB/s, so the time follows the bytes in
the window. The read seeks through the container's index: a window
at 11,700 seconds of a 9.6 GB film took 1.7 seconds. A file with 36
image streams took 3.2 seconds, because one read returns every
stream. No file in the sample was above 9.6 GB. At the same rate a
file of 50 to 80 Mbit/s would take 18 to 29 seconds, which is an
estimate and not a measurement. The stop at 20 lower cues ends most
reads before the window does: on the 24 sampled tracks the first 20
lower cues took 33 to 249 seconds of the title.

## Alternatives that were set aside

- **The player measures at the start of each `Play`.** Every play of
  an image track then pays for the read, and the first cues show at
  the disc's place until it ends.
- **No measurement, and `sub-pos=150`.** mpv then moves every bottom
  cue down until its bitmap meets the bottom edge of the screen. Each
  track ends on the same row, but the row is about 45 rows below the
  house text, and a television that crops the edges of the picture
  can cut it.
- **`mkvextract` or a subtitle decoder of FFmpeg.** FFmpeg has no
  command that writes the bitmaps of a subtitle stream without a
  video to draw them on, and `mkvextract` is one more program in the
  image for bytes that `ffprobe` already returns.

## How the work is proved

On 2026-11-21 the Go decoders and the measurement ran over the packets
of the 24 sampled tracks, outside the repository. Over each track's
600-second sample they gave the same `lineHeight`, `inkBottom`, and
count of cues as the Python that the definitions came from. With the
stop at 20 lower cues, `inkBottom` was the same on 23 tracks and 1 row
lower on one, and `lineHeight` was the same on 19 tracks and 1 row
different on five. The same code then ran with the real `ffprobe` on
one PGS file and two VobSub files that were built from those samples,
and it gave each track's numbers.

Tests decode PGS and VobSub packets that the tests build, and compare
the three numbers with the bitmaps that went in. Tests of the probe
replace `ffprobe` with an answer of their own, as the tests of the
header read do. A drill on a home cluster with a development build
then measures the image tracks of real libraries. The drill's times
and counts go in this header.
