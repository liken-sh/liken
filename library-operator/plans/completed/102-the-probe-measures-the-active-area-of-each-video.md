# 102, The probe measures the active area of each video

Proposed and built on 2026-11-26. The operator's tests and the media
browser's tests pass. No drill has run on a cluster. The measurements
below ran on a laptop with Ubuntu's FFmpeg 8.0.1. The FFmpeg 9.0.2 of
the probe image `library-operator-ffmpeg` 2026.11.19-002 ran one sample
of a 1920x1080 episode and one of a 3840x2160 film with the same
arguments, under Docker on the laptop, and found the same rows as the
laptop's FFmpeg.

[`media-operator` plan 50](../../../media-operator/plans/completed/50-a-two-line-cue-goes-in-the-encoded-bar.md)
uses the fact that this plan records.

## The problem

Some videos have their letterbox bars in the encoded picture. A film of
2.39:1 is stored as a 1920x1080 frame with black rows above and below
it. mpv reads the whole frame as picture, and its `osd-dimensions`
report no bar. So
[`media-operator` plan 49](../../../media-operator/plans/completed/49-a-two-line-cue-goes-in-the-bar-or-above-it.md)
finds no bar on such a video, and a cue of two lines covers the foot of
the picture. On 2026-11-26 the maintainer measured one such episode, a
1920x1080 file, on a 3840x2160 screen: the visible picture ended at
screen row 1895 of 2160, and the ink of a two-line cue was on rows 1859
to 2046.

The player cannot measure the bars when it starts, because the first
frames of a title are often black, and a measurement over the frames of
the film takes seconds that a `Play` should not wait for. The probe
already opens every video once and records what is true of the file.

## The design

### The active area

The active area of a video stream is the rectangle of its frame that
contains picture. Each pixel outside it is black on every frame. The
probe measures it with FFmpeg's `cropdetect` filter, which finds the
smallest rectangle of a frame whose rows and columns have a luma value
above a limit.

The probe reads 8 samples of each video stream, at 1/9, 2/9, and up to
8/9 of the file's duration, so no sample is in the opening titles or in
the end credits. Each sample is one run of `ffmpeg`:

    ffmpeg -nostdin -v error -threads 1 -filter_threads 1
      -noaccurate_seek -ss <time> -i <file> -map 0:<index> -frames:v 1
      -vf cropdetect=limit=0.094:skip=0,metadata=mode=print:file=-
      -f null -

- `-ss` before `-i` seeks through the container's index to the keyframe
  at or before the time. `-noaccurate_seek` makes `ffmpeg` start at that
  keyframe, so it does not decode the frames between the keyframe and the
  time.
- `-threads 1` and `-filter_threads 1` bound the memory. With FFmpeg's
  automatic threads on a laptop with 22 threads, the decode of one
  frame of a 3840x2160 HEVC film took 864 MB. With one thread it took
  268 MB.
- `limit=0.094` is 24/255, FFmpeg's default, as a share of the pixel's
  range. A share is the same for 8-bit and 10-bit video. An absolute
  limit of 24 read the black bars of a 10-bit episode as picture,
  because limited-range black is 64 of 1023. The black of HDR10 and HLG
  video is also 64 of 1023, which is 0.063 of the range.
- `skip=0` makes `cropdetect` measure the first frame. Its default
  skips 2 frames.
- `metadata=mode=print:file=-` writes the frame's `lavfi.cropdetect.x1`,
  `x2`, `y1`, and `y2` keys to standard output. They are the first and
  last column and row of the rectangle, before `cropdetect` rounds them.
  `ffmpeg` can pass a second frame through the filter before
  `-frames:v 1` stops it, and the sample is the union of the frames it
  printed.

A sample whose `x1` is greater than `x2`, or whose `y1` is greater than
`y2`, is a black frame and found no picture.

The active area is the union of the samples that found picture: the
smallest rectangle that contains each of them. A dark scene finds a
smaller rectangle, so the union keeps the area that the brighter
samples found. A film whose shape changes, such as one with IMAX
scenes, gets the largest shape that its samples found, which is correct
because its picture fills those rows in some scenes. When only one
sample is in an IMAX scene, the rule below gives no area, and when none
is, the film gets the area of its wider scenes.

Each of the four edges of the union must be within 2 pixels of the same
edge of at least 2 samples. Otherwise the stream gets no active area.
The rule refuses a union that one sample alone decided, and a union of
dark samples that do not agree on where the picture ends. A file with
no duration gets no active area, because the probe cannot place the
samples.

The samples of one file share one limit of one minute, the limit of the
header read. The probe runs one sample at a time, and each run decodes
one frame. When a run of `ffmpeg` fails or the limit ends, the probe
records what the header read found, and the attempt is an error. The
record then lacks the measurement, so the probe reads the file again
after the error's period.

### What the measurement found

On 2026-11-26 the laptop measured 45 files from a home library over NFS,
one file at a time. Most files got 8 samples, and 5 films got 9.

| Files | Frame | Active area |
|---|---|---|
| 8 episodes of one series | 1920x1080, HEVC 10-bit | rows 131 to 947 |
| 2 episodes, each of another series | 1920x1080 | rows 138 to 941 |
| A film of about 2.4:1 | 1920x1080 | rows 140 to 939 |
| A film | 3840x2160, HEVC 10-bit | rows 263 to 1896 |
| A film in HDR10 | 3840x2160 | rows 42 to 2117 |
| 2 films of about 1.85:1 | 1920x1080 | rows 21 to 1058, and 20 to 1058 |
| A film of 2:1 | 1920x1080 | rows 60 to 1019 |
| A film with black sides | 1920x1080 | columns 239 to 1680 |
| 3 films with IMAX scenes | 1920x1080 or 1920x1012 | the whole frame |
| A found-footage film | 1920x1080 | none |
| 24 files stored at their own shape, including HDR10, 4:3, and 3840x1600 | various | the whole frame |

On each file that got an area smaller than its frame, at least 5
samples agreed on the lower edge. The 1920x1080 episode that the
maintainer measured on the screen is one of the 8 episodes in the
first row. Its lower bar is rows 948 to 1079, and row 1895 of a
2160-row screen is row 947 of the frame.

The 3 films with IMAX scenes had samples of about 2.39:1 and samples
of the whole frame, and their union is the whole frame. The samples of
the found-footage film started between rows 22 and 37 and ended between
rows 1046 and 1057. No two of them agree on the top edge or on the
lower edge, so the film gets no active area and plays as it does today.

### What a measurement costs

The final command ran on 16 files that no earlier run had read: 11
episodes and 5 films, each 1920x1080 or smaller. The 8 samples of a file
took 1.1 to 2.3 seconds in all, and the largest `ffmpeg` process used
112 MB. On the 4 films of 3840x1600 or 3840x2160, one sample took 0.27
to 0.66 seconds on average, so 8 samples take about 2.2 to 5.3 seconds,
and the largest process used 275 MB. An earlier run with 2 frames for
each sample and FFmpeg's automatic threads took 1.2 to 4.5 seconds for
a 1920x1080 file and 4.7 to 9.8 seconds for a 3840x2160 file.

`ffmpeg` is a child process of the probe container, so its memory counts
against the container's limit. In the probe image, the sample of the
3840x2160 film finished under a memory limit of 256 MB and was killed
under 200 MB. The probe's own process took 30 MiB in the drill of plan
97. So the probe container's limit rises from `256Mi` to `512Mi`.

### The record

A video stream's entry in `.liken/probe.yaml` gains one optional block,
in pixels of the stream's frame:

    activeArea:
      left: 0
      top: 131
      width: 1920
      height: 817

The corner is `left` and `top`, not `x` and `y`, because YAML 1.1 reads a
bare `y` as a boolean, and the ledger's YAML library then quotes the key.

The file's record gains `activeAreaMeasurement: 1`, the version of the
measurement, which every record has that a probe with this measurement
wrote. A record whose version is lower and that holds a video stream
opens the probe gap again, the way a record without `counted: true`
does, so the probe reads every video once more. A video stream with no
`activeArea` in a record of the current version gave no area that the
samples agree on, and the probe does not read it again. A later change
to the definitions raises the version, and the probe then reads every
video once more.

### The catalog and the play request

The `streams` table gains four nullable integer columns at its end:
`active_left`, `active_top`, `active_width`, and `active_height`. A null
means that the stream is not a video stream, that the probe has not
measured it, or that its samples did not agree.

The media browser adds `video` to each item's `presentation`, with one
entry for each video stream of the item's file, in container order:

    "video": [{"stream": 0, "width": 1920, "height": 1080,
               "activeArea": {"left": 0, "top": 131, "width": 1920, "height": 817}}]

`stream` is the stream's index in its container, which mpv calls
`ff-index`. `width` and `height` are the frame's size from the probe, so
the display can scale the rows of the area to the rows of the screen.
`activeArea` is absent when the four columns are null. A file that the
probe has not read has no stream rows, so its item has no `video`. The
operator copies the list onto the `Play` unread, as it copies `audio`.

## The probe reads every video again

When the build runs, each walk of a `Library` reads every probe record.
Each record that holds a video stream and has no
`activeAreaMeasurement: 1` opens the probe gap for its file, and the
probe container of the same `Job` reads the file again. At the times
above, the samples of 10,000 videos that are mostly 1920x1080 take about
3 to 7 hours of probe time in all. That is an estimate from the
laptop's runs, and the drill measures it.

A walk `Job` has a deadline of two hours, and a `Job` that reaches it
fails with `DeadlineExceeded`. Its close container then never runs, so
the walk's webhooks of plan 99 are never posted, and the webhooks of
folders wait behind each such `Job`. So the probe is a time-limited
phase, as the trailer files are: 15 minutes after the phase starts, it
finishes the file it has, starts no other, and ends. The `Job` closes as
usual, and the next walk goes on with the gap. One file takes at most
four minutes of read limits, a minute each for the header read, two
subtitle windows, and the samples, so the `Job` ends well inside its
deadline.

The re-read therefore spreads over several walks. At 2 to 3 seconds a
file, one walk reads about 300 to 450 videos, so a library of 10,000
videos takes about 25 to 35 walks, a day or two at the default hourly
`spec.scan.schedule`. `kubectl liken library rescan <name>` starts a walk
at once. Each `Library` has its own probe, so the libraries work through
their gaps at the same time.

Within each run, the probe reads each new or changed file before each
file whose record describes it at its size and modified time. Such a
file is in the gap only for a fact that its record lacks, so a new
download does not wait behind the backlog. The order is the probe's own,
from the ledger of each folder, because the gap query cannot tell a file
that no probe read from a record that lacks a fact: both have a `probed`
column of 0.

A file read again keeps each measurement that its record has at the
current version, when the record describes the same file with the same
streams. So the re-read is the header read and the 8 samples, and the
windows of plan 97 are not read again. A window that fails during the
re-read cannot replace a good image measurement with none.

## Alternatives that were set aside

- **The display measures at the start of each `Play`.** mpv's own
  `vf-metadata` of a `cropdetect` filter would need several seconds of
  frames, the first frames of a title are often black, and each play
  of a title pays again.
- **One sample, or samples from the start of the file.** Opening titles
  and fades are black, and one dark scene shrinks the area.
- **The intersection of the samples, or their median.** A dark scene
  then moves an edge into the picture, and a cue then covers picture.
- **`-skip_frame nokey`.** `ffmpeg` then reads to the next keyframe
  before `-frames:v 1` stops it, which took about twice as long on a
  1920x1080 episode.
- **`ffprobe` with a `movie` source.** The file's path goes inside a
  filter graph and needs escaping, and `ffmpeg` is in the probe image.

## How the work is proved

Tests on the fake clock check that the probe starts no file after its
time limit, and other tests check the order of the gap and the
measurements that a re-read keeps. Tests read recorded `cropdetect`
output and check the sample's box, the
black frame, the union, the agreement of the edges, and the arguments of
each run. Tests of the probe replace `ffmpeg` with an answer of their
own, as they replace `ffprobe`. Tests of the walk check that a record
without the version opens the probe gap once, and that the four columns
carry the area. Tests of the media browser check the `video` list of a
play request, and a test of the operator checks that the list reaches
the `Play`. A drill on a home cluster with a development build then
records how long the walks take to read every video again, and the
drill's times go in this header.
