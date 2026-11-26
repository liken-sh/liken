# 49, A two-line cue goes in the bar or above it

Proposed and built on 2026-11-26. The display's tests pass. No drill
has run on a cluster. A drill on `stick-1` has not checked the copy on
a real screen, and nobody has measured how long the read of a film's
cues takes over the cluster's network share.

Changed on 2026-11-26: [plan 50](50-a-two-line-cue-goes-in-the-encoded-bar.md)
adds to the bar the black rows inside a frame that has its letterbox
bars in the encoded picture, from the active area that
`library-operator`'s probe measures.

[Plan 46](46-the-display-places-every-subtitle-track.md) ends the last
line of every subtitle cue on the house row, and its "Limits" section
records the problem that this plan solves for text tracks: a two-line
cue covers the foot of the picture on a film with bars.

## The problem

On a film with a lower bar, a one-line text cue ends on the house row
and is entirely in the bar. The maintainer wants each one-line cue to
stay where it is on every title. A cue of two lines grows up from the
same row, so its top line crosses the lower edge of the picture.

Renders on the laptop on 2026-11-26 measured the rows. They ran mpv
0.41.0 in the house style of `pod.go`: Source Sans 3 at size 38,
outline 1.65, and `sub-margin-y=34`. Each row counts the white of the
text and not its outline. Through `--vo=image`, on a 1920x1080 frame
whose 1920x800 picture had bars in the encoded video, at `sub-pos=100`:

| Cue | Rows of each line |
|---|---|
| One line | 984 to 1026 |
| Two lines | 927 to 969, 984 to 1026 |
| Three lines | 870 to 912, 927 to 969, 984 to 1027 |

So one line of ink is 44 rows tall, from the top of a capital to the
foot of a descender, and the lines are 57 rows apart. The lower bar is
rows 940 to 1079, so the top line of a two-line cue is 13 rows into the
picture.

## Moving each cue with `sub-pos` was set aside

The first build observed `sub-text` and set `sub-pos` for each cue by
its number of lines. A reviewer measured with mpv 0.41.0 that mpv sends
`sub-text` only after the frame that first draws a new cue. So every
two-line cue was drawn at the house row for a frame or more and then
dropped about 33 rows. A one-line cue that starts when a two-line cue
ends was first drawn about 33 rows low, and in one episode 174 of 560
cues start that way. One-line cues must not move, so the display does
not set `sub-pos` for each cue.

## The design

Each cue carries its own vertical margin in the subtitle data, so
libass places it on the first frame that draws it.

### The copy

When the selected track is a text track, the display reads every cue of
the track and builds an ASS script of them. It writes the script to a
file in the directory of mpv's socket, the pod's `/ipc` `emptyDir`,
which the player container mounts too and which is deleted with the
pod. It gives the file to mpv with `sub-add <file> select <title>
<lang>`, with the title and the language of the original. The file's
name is `media-display-copy.<identity>.<number>.ass`. The identity is
`ff-index-3` for a track inside the video and `external-5` for an
external track, by the id that mpv gives it. mpv reports the file as
the copy's `external-filename`, so the display finds the original of a
copy from the name. The track panel lists the original once, as
selected while mpv draws the copy. When a person selects the original
in the panel, the display selects the copy again.

The copy goes through a file because of the size of `track-list`. mpv
reports a track's file name as its `external-filename` in every
`track-list`, and `track-list` goes to the display and to the command
sidecar each time a track changes. A script given as `memory://` is its
own file name, so each `track-list` would carry every cue of the film.
The display removes a copy's file when mpv no longer draws that copy,
when another track or another file is selected, when mpv exits, and when
the display's window closes. The media is mounted read-only, and the
display never writes it.

The copy of a SubRip, WebVTT, `mov_text`, or MicroDVD track has the
header that mpv gives the original. mpv draws such a track through
FFmpeg's converter, and `configure_ass` in mpv 0.41.0's
`sub/sd_ass.c` then changes the converted script's default style for
each size of the screen. `mp_ass_set_style` sets the house style in a
`PlayResY` of 288, so `sub-margin-y=34` becomes a `MarginV` of 13 and
`sub-margin-x=19` a margin of 7, as libass stores integers. `PlayResX`
becomes 288 times the shape of the picture. `MarginL` and `MarginR` are
scaled by `PlayResX ÷ 384`, and `MarginV` by the font scale of
`sub-scale-with-window`, the height of the screen over the height of
the picture, each rounded. mpv does not change an ASS track, so the
display writes those values into the copy's header for the screen as
it is now. The display draws the copy as mpv draws the original, at
`sub-pos` 100, with `sub-ass-override=force` for the house style.

The copy of an ASS track keeps the script's own header and every field
of each event. mpv reports the copy as an ASS track, so plan 46 places
it as one: `sub-ass-override=force` and the resting `sub-pos` of
`ass_rest`, read from the copy's header.

The markup of each format is converted as
FFmpeg's decoders convert it for mpv: `<i>`, `<b>`, `<u>`, `<s>`, and a
`font` colour for SubRip, `<i>`, `<b>`, and `<u>` and the entities for
WebVTT, `|` and the `{y:...}` codes for MicroDVD, and each line break
as `\N`.

### Where the cues come from

Each source is one module under `display/src/subtitles/copy/`. The
display finds the track by mpv's `src-id`. mpv 0.41.0 reported the
`TrackNumber` of a Matroska track as its `src-id`, because mpv reads
Matroska with its own demuxer, and the `track_ID` of an MP4 track, which
FFmpeg gives as the stream's id. On a file that FFmpeg made, the
subtitle track's `src-id` was 2 and its `ff-index` 1 in both containers.

- **Matroska** (`matroska.rs`): the EBML header, the `Segment`, each
  `SeekHead` before the first cluster and each `SeekHead` that a seek
  names, then `Info`, `Tracks`, and `Cues`. The track is the
  `TrackEntry` whose `TrackNumber` is the `src-id`. The reader refuses
  the track when the entry's codec is not the one that mpv reports, or
  when its blocks are compressed. Each `CuePoint` of the track gives the
  cluster and the place of one block in the cluster's data. The reader
  reads each cluster's header for its timestamp and then each block, 16
  reads at a time with `pread`. An ASS copy keeps each event's
  `ReadOrder`, so events that show at once stack as in the original.
- **MP4** (`mp4.rs`): `moov`, then the `trak` whose `tkhd` has the
  `src-id` as its `track_ID`, which must be a `tx3g` track. `stts`,
  `stsz`, `stsc`, and `stco` or `co64` give each sample's time, size,
  and place, and `mdhd` the timescale. The reader applies the usual edit
  list: the time of empty edits before the first edit is added, and that
  edit's media time is subtracted. It reads the samples 16 at a time.
  The bold, italic, and underline of each sample's `styl` box become
  ASS overrides, as FFmpeg's `movtextdec` writes them inline. mpv draws a
  `mov_text` track as a converted track, so `configure_ass` puts the
  house style and the house alignment on it, and the track's default
  style and justification do not reach the screen. The copy leaves them
  out too.
- **External files** (`external.rs`): mpv's `external-filename`, read
  whole and converted by the codec that mpv reports: `subrip`, `ass`,
  `ssa`, `webvtt`, or `microdvd`. A MicroDVD file is timed by its first
  line or the video's `demux-fps`. A byte-order mark names UTF-8 or
  UTF-16, a file without one is UTF-8 when it decodes as UTF-8, and
  otherwise Windows-1252. SubRip times take a comma, a full stop, or a
  colon before the milliseconds, and a cue keeps only its first `{\anN}`
  override, as FFmpeg reads them.

Every other track keeps the placement of plan 46, and the display logs
one line with the reason. The display reads nothing until mpv reports
`osd-dimensions` and `demuxer-start-time`. mpv plays a file from its
start time as 0 and moves the packets of the file's own tracks by it,
but not a subtitle file that it adds. On 2026-11-26 mpv 0.41.0 drew a
`.srt` file beside a video that starts at 10 s, and the same cues inside
that video, at the same playback times. So the copy of an embedded
track moves each cue back by the start time, and the copy of an
external file keeps its times. The copy of the embedded track of that
video drew its cues at the same playback times as the original. The
read runs off the drawing thread, and the original plays with the
placement of plan 46 until the copy is ready. Each read has a number,
and a read stops at its next read of the file once a newer read starts,
so a track switch does not leave the earlier read's threads reading.
`sub-delay` is an mpv option that applies to the selected track, so it
applies to the copy. The display builds the script once for each screen
and writes and removes the files off the drawing thread. When mpv drops
the copy, as when it opens a file again, the display adds it again, up to
3 times. The command sidecar leaves a copy out of its choice of a track,
by the file name.

### A copy never loses a cue

A copy that misses a cue would make a subtitle vanish, so the display
makes no copy unless it reads every cue. One block or sample that
cannot be read, a laced block, a cluster header without a timestamp, a
cue point without its place, text that is not UTF-8, or an element that
the file cuts off fails the whole read, and the original keeps the
placement of plan 46. A copy with no cue is refused. When the item's
presentation gives the track's event count, the `NUMBER_OF_FRAMES` tag
that mkvmerge writes for each block, the copy must have that many cues
within the larger of 2 and 1%, or it is refused.

Every size and count in a file is a number that the file states, and an
allocation that fails ends the process. So each read is at most 64 MiB,
a Matroska block or an MP4 sample at most 1 MiB, `SeekHead`, `Info`, and
`Tracks` at most 16 MiB, and an MP4 track at most 1,000,000 samples. A
table that states more entries than its box holds fails the read. A
reader thread that panics fails the read, so no result is lost or moved
to the wrong cue.

Measured by the coordinator on the laptop over NFS: the `Cues` element
of a 3.45 GB episode read in about 0.1 s, and 692 cues fetched 16 at a
time in 0.27 s. Read one at a time, the cluster headers took about
14 s. Files made by mkvmerge and by FFmpeg (Lavf62) in a home library
index every subtitle block.

### The margin

An event of one line keeps `MarginV` 0, which is the style's margin, so
it ends where it ends today. An event of two or more lines, on a
picture with a lower bar, gets a `MarginV` of its own unless the script
places it itself: an event with a margin of its own, a style at the top
or in the middle, `\pos`, `\move`, or an `\an` or `\a` code at the top or
in the middle keeps its margin.

The arithmetic is in rows of a 1080-row screen, because mpv scales the
text with the height of the screen. `bar` is the lower bar from
`osd-dimensions` in those rows, and `lines` counts `\N` in the event.

    ink    = 44 + 57 × (lines − 1)
    end    = the resting row − 4          the row under the last ink row
    edge   = 1080 − bar                   the first row of the bar
    move   = (edge + 1080) ÷ 2 − (end − ink ÷ 2)  when bar ≥ ink + 2 × 12
    move   = min(0, edge − 12 − end)               otherwise
    target = the resting row + move

Under `force`, libass ends a cue with margin `m` at row
`sub-pos ÷ 100 × (1080 − m × picture ÷ PlayResY)`, where `picture` is
the picture's height in rows. The resting row is that row for the
style's margin, and `end` above is 4 rows above it. The event's margin
is the equation solved for `target`, rounded. A margin of 0 means the
style's margin, so a result that rounds to 0 takes 1 or −1. libass
draws a negative margin below the foot of the frame, which a deep bar
needs when the script's own margin is large.
When `osd-dimensions` changes, the display builds the script again,
adds it, and removes the old copy once mpv draws the new one.

A space of 12 rows keeps the outline of the top line off the picture
and lets the bar of every film of about 2.31:1 or wider hold two
lines, including the scope films of 2.35:1 to 2.40:1. A film of 2.20:1
has a bar of 104 rows, which is not enough for the 101 rows of ink of
two lines, so the cue goes onto the picture.

| Picture | SubRip copy: `PlayResX`, side margins, `MarginV` | Two lines, SubRip | Two lines, ASS `PlayResY` 804, `MarginV` 20 |
|---|---|---|---|
| 1920x800 | 691, 13, 18 | 6 | −14 |
| 1920x804 | 687, 13, 17 | 5 | not computed |
| 1920x872 | 634, 12, 16 | 37 | 81 |
| 1920x1038 | 532, 10, 14 | 0 | 0 |
| 1920x1080 | 512, 9, 13 | 0 | 0 |

Three lines of a SubRip copy on 1920x800 get 53. The tests of
`copy/converted.rs` and `cue.rs` state these values.

### Renders of the copy

A headless `cage` with a 1920x1080 output ran mpv 0.41.0 with
`--vo=gpu` on 2026-11-26, so mpv saw a real picture with bars. For each
picture height, FFmpeg muxed a SubRip file and an ASS script with
`PlayResY` 804 and `MarginV` 20 into a Matroska file. The display's
reader read each file and wrote its copy for that screen. The original
was drawn as mpv draws it today: the SubRip track at `sub-pos` 100, the
ASS track under `force` at its `ass_rest`. The copy was drawn as the
display draws it: the SubRip copy under `force` at `sub-pos` 100, the
ASS copy under `force` at the same `ass_rest`. Each cell is the rows of
the white of each line.

| Picture | Cue | SubRip original | SubRip copy | ASS original | ASS copy |
|---|---|---|---|---|---|
| 1920x800 | one line | 982–1025 | 982–1025 | 984–1027 | 984–1027 |
| 1920x800 | two lines | 925–1025 | 959–1058 | 927–1027 | 960–1060 |
| 1920x804 | one line | 985–1028 | 985–1028 | 984–1027 | 984–1027 |
| 1920x804 | two lines | 928–1027 | 961–1061 | 927–1027 | 960–1060 |
| 1920x872 | one line | 984–1027 | 984–1027 | 984–1027 | 984–1027 |
| 1920x872 | two lines | 927–1026 | 863–963 | 927–1027 | 863–962 |
| 1920x1038 | one line | 982–1025 | 982–1025 | 984–1027 | 984–1027 |
| 1920x1038 | two lines | 925–1024 | 925–1024 | 927–1027 | 927–1027 |
| 1920x1080 | one line | 984–1026 | 984–1026 | 984–1027 | 984–1027 |
| 1920x1080 | two lines | 927–1026 | 927–1026 | 927–1027 | 927–1027 |

Every one-line cue of a copy is on the rows of the original. The
original SubRip track moves by up to 3 rows from one picture height to
the next, because mpv rounds `MarginV` after it scales it, and the copy
has the same rounded margin. A two-line cue of a copy has 18 to 21 rows
of black above and below it in a bar of 138 or 140 rows, and leaves 12 or
13 rows of picture under it on 1920x872. One step of `MarginV` in a `PlayResY`
of 288 is 2.8 rows on a 1920x800 picture, so the space is within a row
or two of 12 and 20. A three-line SubRip cue on 1920x800 drew rows 772
to 928, which leaves 11 rows of picture under it.

An earlier build gave the SubRip copy FFmpeg's header with a fixed
`MarginV` of 13 and placed it with `ass_rest`. Its one-line cue was
drawn on rows 984 to 1027 at every picture height. Against the original
it was 2 rows lower on 1920x800 and 1920x1038, 1 row lower at full
height, and 1 row higher on 1920x804. That build was not kept.

## Limits

- **mpv's own steps.** The copy's header repeats what mpv 0.41.0 does to
  a converted track. A later mpv that changes `configure_ass` changes the
  original's rows and not the copy's, so a bump of mpv repeats the renders
  above.
- **A line that libass wraps is not counted.** Only `\N` counts, so a
  long line that libass wraps at the side margins counts as one line.
- **Image tracks.** A PGS or VobSub cue is a picture, and the display
  cannot write a margin into it, so a two-line image cue still covers
  the foot of the picture.
- **Files without index entries.** A Matroska file with no cue point
  for the track, compressed blocks, laced blocks, or a cue point without
  `CueRelativePosition`, a video in another container, and a stream
  from the network keep the placement of plan 46. So does any file where
  one cue cannot be read, as "A copy never loses a cue" says.
- **The start of play.** Until the read finishes, mpv draws the
  original with the placement of plan 46. The coordinator's timings
  above add up to under a second for one episode, but nobody has timed
  the parallel read of the cluster headers. So the first second or two
  can show a two-line cue across the picture.
- **A copy is on disk while it plays.** The `/ipc` `emptyDir` is on the
  node's disk, not in memory, so the copy's file does not count against
  the pod's memory. A display that is killed leaves its files until the
  pod is deleted.
- **An edit list** that repeats or skips parts of an MP4 track is not
  followed.
- **A legacy encoding** other than Windows-1252 in an external file can
  read differently from mpv's guess with uchardet. Its line breaks are
  the same.
- **The event count** is checked only where the presentation has one.
  An external file and a file that mkvmerge did not write have none.

## Alternatives that were set aside

- **Per-cue `sub-pos`**, for the reason in its section above.
- **A smaller font for a two-line cue.** The house size is the same
  for every cue, and the maintainer chose to keep 38.
- **Moving every cue into the middle of the bar.** One-line cues
  already look correct on the house row, and the maintainer does not
  want them moved.

## How the work is proved

Tests of the display build small Matroska and MP4 files in memory and
read their cues, find the track by its `src-id`, convert each text
format and encoding, compute the margin for the screens of the table,
and check the commands: `sub-add` with `select`, one entry in the track
panel, the selection of the copy, a new copy for a new screen, a dropped
read after a track switch, a copy added again, and the placement of
plan 46 when a track has no index or a cue cannot be read. A copy of a
file that starts late is made, with its cues moved back. Hostile and
truncated Matroska and MP4 files, with sizes and counts up to 2^52 and
2^32 − 1, fail the read without an allocation of that size. A test of
`subtitletracks.go` checks that the command sidecar does not choose a
copy. A drill on `stick-1` then plays a film of 2.39:1 with a SubRip
track and an ASS track, and a capture checks the rows of one-line and
two-line cues and the time until the copy is drawn.
