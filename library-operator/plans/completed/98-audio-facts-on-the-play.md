# 98, Audio facts on the Play

Proposed and built on 2026-11-23. The tests of the media browser, of
`media-screen`, and of both operators pass. No drill has run on a
cluster.

## The problem

The film's display in `media-operator` names each audio track in its
track panel, which
[`media-operator` plan 48](../../../media-operator/plans/completed/48-one-panel-for-a-track-and-its-timing.md)
designs. A name such as "E-AC3 Atmos 5.1" needs the codec, the profile,
and the channel layout of the track. mpv reports the profile and the
layout only of the audio track that it decodes. On mpv 0.41, the three
audio tracks of a test film that mpv was not decoding read
`demux-channels: unknown2` and had no `codec-profile`. mpv's source
sets the two properties in `ad_lavc.c` and `ad_spdif.c` when the
decoder gives its first frame, so a track that is not decoded never
gets them. The display cannot name the format of those tracks from mpv.

`library-operator`'s probe already records the codec, the profile, the
channel count, and the layout of every audio stream in the catalog's
`streams` table, and the media browser's files section names each
track from those rows.

## The contract

Each item's `presentation` in a `Play` has `audio`, a list with one
entry for each audio stream of the item's file, in container order:

```yaml
audio:
  - stream: 1
    codec: eac3
    profile: "Dolby Digital Plus + Dolby Atmos"
    channels: 6
    layout: "5.1(side)"
```

- `stream` is the stream's index in its container, as ffprobe gives
  it. The display matches it to the `ff-index` of a track in mpv's
  `track-list`.
- `codec` and `profile` are ffprobe's codec name and profile, and
  `layout` is ffprobe's channel layout. `channels` is the channel
  count.
- The media browser leaves out a value that ffprobe did not report: an
  empty text and a count of zero. Only `stream` is required.
- A file that the probe has not read has no stream rows, so its item
  has no `audio`. The display then names each track from what mpv
  reports.

The media browser writes the list where it writes
`presentation.subtitles`, from the probe's rows. `library-operator`
carries the list unread from the play request to the `Play`, and
`media-operator` carries it unread from the `Play` to the display's
presentation block, the same way as `subtitles`.

## One copy of the names

The display needs the names that the media browser already gives a
track: the English name of its language, the role of a subtitle track
from the subtitle rule of root plan 90, and the format words. Those
were Rust code in the browser. They are now in the `media-screen`
crate, which the browser and the display both link by path, so a track
has the same name on both screens:

- `media_screen::language` has `canonical`, `same`, and `name`.
- `media_screen::tracks::subtitle` has `Track`, `Role`, `Classified`,
  `classify`, and `file_name`. `Role::label` is the word a screen draws
  for a role.
- `media_screen::tracks::format` has `audio` and `subtitle`, which take
  ffprobe's values and not the browser's `Stream` type.

The choice of a subtitle track stays in the browser's `tracks/`. It
reads the browser's `Preferences`, and the display does not choose a
track, because `media-operator`'s command sidecar does that in Go. The
tests of `media-screen` read the classify, file name, and language
cases of `media-operator/testdata/subtitle-rule.json`, and the tests of
the browser read its choose cases.
