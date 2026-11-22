# 97, The probe measures image subtitle tracks

Proposed and built on 2026-11-21. The operator's tests pass.

Changed on 2026-11-22, after a drill on a home cluster with a
development build. The probe measured 9,825 image streams in 2,121
files. The streams with a `lineHeight` of 100 thousandths of the canvas
or more were two lines that touch, boxes behind the text, and pictures,
which the first definitions read as one line of text. "The second
measurement" below has the evidence, the changed definitions, and what
happens to the records that the first build wrote. The same drill found
that a probe stopped by a restart recorded an error attempt for the
file it was reading, which kept the file out of the probe's work for a
day. The probe now records no attempt for a read that its own shutdown
ended.

A drill of the second measurement ran on 2026-11-22 on the same home
cluster, with `library-operator` 2026.11.21-002-dev-002. The walks
took each first-build record's numbers out of the catalog, and the
probe read 2,077 files again in about 75 minutes: a median of 1 second
and a mean of 2.5 seconds for an episode file, and a median of 3
seconds and a mean of 8.5 seconds for a film file. 9,722 streams have
numbers, where the first measurement gave 9,825. 7 streams measure a
`lineHeight` of a tenth of the canvas or more, where 285 did, and 33
measure 8% or more, where 314 did. Nobody has looked at those 7. A
testbed that shares the volume restarted twice during the two drills
while it ran the first build, and each time its probe recorded an
error attempt for the file it was reading. Those two files wait out
the error's period on both clusters.

The drill of the first build ran on 2026-11-22 with `library-operator`
2026.11.21-001-dev-002 on a home cluster whose nodes read the NAS at
about 100 MB/s. The walks of two libraries started together, and the
probe read 2,086 files in about 80 minutes, with both probes on the NAS
for most of that time. For the 1,561 episode files the time from one
file's record to the next had a median of 1 second, a mean of 3.0
seconds, and a 90th percentile of 5 seconds. For the 525 film files it
had a median of 3 seconds, a mean of 7.7 seconds, and a 90th percentile
of 14.5 seconds. In each library 8 files took longer than 60 seconds.
The probe container used 30 MiB of its 256 MiB limit and did not
restart. 9,303 of 9,685 PGS streams and 522 of 578 VobSub streams got
numbers, and 36 files have no measured stream. On a testbed whose node
reads the same NAS at 10 MB/s, a film took about 20 seconds. The
records are beside the media, so the catalog of the first cluster took
the 38 files that the testbed had measured without a read.

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

## The second measurement

### What the drill found

On 2026-11-22 the probe of a home cluster had measured 9,825 image
streams. The table gives `lineHeight` in thousandths of `canvasHeight`.

| Thousandths | Streams | Files |
|---|---|---|
| 30 to 39 | 78 | 38 |
| 40 to 49 | 353 | 123 |
| 50 to 59 | 8,108 | 1,653 |
| 60 to 69 | 797 | 403 |
| 70 to 79 | 175 | 152 |
| 80 to 99 | 29 | 24 |
| 100 to 149 | 269 | 176 |
| 150 and over | 16 | 16 |

A `Job` read the packets of 12 of the streams outside the usual range,
in the probe's first window, and the Go decoders turned them into
bitmaps on a laptop.

| Range | Streams read | What the bitmaps are |
|---|---|---|
| 100 to 149, PGS | 3 English, 1 Japanese | Two lines that touch. Each line has a thick outline, and the outline of a descender in the first line meets the outline of an ascender or a capital in the second, so no row between the lines is clear. A cue of two such lines is one run of ink rows 1.84 to 2.01 times as tall as one line with a descender. |
| 100 to 149 and 150 and over, VobSub | 2 English | An opaque box behind each line, with the letters cut out of it. The boxes of the lines of one cue touch, so a cue of two lines is one run of 125 to 136 rows and a cue of three lines one run of 199 to 211 rows. |
| 150 and over, PGS | 2 English | Pictures: a track of photographs that goes with a commentary, and a track of trivia drawn on graphics. Each picture is 394 to 697 rows tall. |
| 80 to 99, PGS | 1 English, 1 Thai | One line in each case. The English track is a forced track in a large decorative font, 97 to 104 rows a line. A line of Thai has vowel and tone marks above and below its letters, and is 92 to 98 rows. The numbers are correct. |
| 30 to 39, PGS | 2 English | Small fonts, in an SDH track and a commentary track, 38 to 40 rows a line with descenders. The numbers are correct. |

The 24 tracks that set the first definitions had no box, no picture,
and no two lines that touch, so the first definitions did not provide
for them.

### The changed definitions

The canvas, the ink rows, the cues, and the lower cues are as above.
These definitions replace the definitions of a line, `lineHeight`, and
`inkBottom`.

- A row is **gapless** when every pixel from its first ink pixel to its
  last is ink. A row of letters has clear pixels between its letters
  and its words. A row of a box or a picture usually has none.
- A **line** is a run of consecutive ink rows in which fewer than three
  of each four rows are gapless, and which is at most a quarter of the
  canvas tall. Any other run of ink rows is a box or a picture. It is
  in no number, and a bitmap with no line is not a cue.
- A line at least 1.7 times the median height of every line is **two
  or more lines that touch**.
- `lineHeight` is the 90th percentile, by nearest rank, of the heights
  of the lines that are not two or more lines that touch.
- `inkBottom` is the median, over the lower cues whose last line is not
  two or more lines that touch and is at least `lineHeight − 1` rows
  tall, of the row just below that last line. When no lower cue has
  such a line, every lower cue counts.

The decoders reduce each row of a bitmap to its largest alpha, its
count of ink pixels, and the columns of its first and last ink pixel,
so the probe still keeps no picture.

The limits come from the streams above. Of the lines of text, the most
gapless one had 54% of its rows gapless, a short line in a thick
outline. On the 24 tracks the most was 39%. Every run of the two box
tracks had 100%. Nine of the 11 pictures had 100%. One graphic with
clear areas around it had 64%, and one photograph with many clear areas
had 10%. They are 635 and 421 rows tall, more than a quarter of the
canvas, and the limit of a quarter is what keeps them out. The tallest run
of text on the streams above was 200 rows, three lines that touch on a
960-row canvas, whose quarter is 240 rows. Some runs of the Thai track are a mark
alone, and they are gapless. They are not lines, and the Thai track's
numbers are the same with them and without them.

Two lines touch only where a descender meets an ascender or a capital,
so a run of two lines is at least the height of one full line and one
line with no descender: 1.84 times one full line on the tracks above.
The median line of a track is a full line or a line without a
descender, so the run is at least 1.84 times the median. The tallest
line of the Thai track was 1.53 times its track's median, and the
tallest line of the decorative font 1.07 times. A limit of 1.7 is
between them.

The median is a line only while fewer than half of the runs of a track
are two lines that touch. On the four tracks of two lines that touch,
such runs were 14% to 33% of the runs. A track with more than half
would still give the height of two lines.

### The records of the first measurement

A record that the probe writes has `measurement: 2` beside
`measured: true`. The first build wrote `measured: true` alone, and a
build that reads only `measured` still reads a new record as measured.

| Record | The walk | The probe |
|---|---|---|
| No `measured`, with an image stream | Opens the probe gap, as before. | Reads the file once. |
| `measured: true` with no `measurement`, with an `image` block on any stream | Leaves every `image` block of the record out of the stream rows, so the three columns are null, and opens the probe gap. | Reads the file once and writes `measurement: 2`. |
| `measured: true` with no `measurement`, with no `image` block | No change. | Does not read the file. |
| `measurement: 2` | No change. | Does not read the file. |
| No image stream | No change. | Does not read the file. |

A stream with no block had too few cues for the first definitions, and
the second definitions count the same cues or fewer, so it has too few
again. A stream with a block can change. The three numbers do not show
whether a track was a box, a picture, or a track with some lines that
touch, so the probe measures each of them again. Until it does, the
player draws the track where the disc placed it.

On 2026-11-22 the catalog of the home cluster had 2,121 files with an
image stream, and 2,085 of them had a block on at least one stream. The
probe reads those 2,085 files again, and the other 36 files not. A
stream of boxes or pictures has no lower cue, so the probe reads both
of its windows to their ends.

### How the second measurement is proved

The changed decoders and measurement ran over the packets of the 24
sampled tracks, outside the repository. Each of the three numbers was
the same as with the first definitions, over each track's 600-second
sample and with the stop at 20 lower cues.

Over the probe's first window of each of the 12 streams above, the
first definitions and the second gave these numbers. The catalog
column is the probe's own result, which can include a second window.

| Stream | Kind | Catalog `inkBottom`, `lineHeight` | First definitions | Second definitions |
|---|---|---|---|---|
| tall-1 | Two lines that touch, 1080 rows | 1061, 150 | 1061, 150 | 1059, 75 |
| tall-2 | Two lines that touch, 960 rows | 942, 134 | 942, 134 | 941, 68 |
| tall-3 | Two lines that touch, 800 rows | 780, 112 | 783, 112 | 783, 56 |
| tall-4 | Two lines that touch, Japanese | 929, 136 | 929, 136 | 929, 69 |
| box-1 | VobSub boxes | 977, 199 | 977, 137 | none |
| box-2 | VobSub boxes | 988, 135 | 988, 135 | none |
| picture-1 | Photographs | 1023, 597 | none | none |
| picture-2 | Trivia graphics | 1007, 467 | none | none |
| large-1 | Decorative forced font | 907, 104 | 904, 101 | 904, 101 |
| thai-1 | Thai | 913, 92 | 913, 92 | 913, 92 |
| small-1 | SDH | 971, 40 | 971, 40 | 971, 40 |
| small-2 | Commentary | 964, 38 | 964, 38 | 964, 38 |

The picture streams had too few cues in the first window for any
numbers, and the probe's numbers came from both windows. With the
second definitions they have no cue at all.
