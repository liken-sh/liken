# 90, Subtitle tracks by role

Proposed and built on 2026-11-08. The tests of both operators pass.
A drill on the testbed's `lab-portable` Player ran on 2026-11-08 with
`media-operator` 2026.11.08-002, `subtitles: on`, and `sdh: prefer`.
On a Play of two episodes, the sidecar chose the 1,382-event track of
Hawkeye S01E06 over its 22-event default track and its empty track,
from the counts on the item. On the second item, through mpv's
`file-loaded` event and with no counts, it chose the SDH track of
Pluribus S01E02 over its forced default track. No drill has run of
`subtitles: auto`.

## The problem

A household that sets `subtitles: on` with English audio and English
subtitles sees a forced English track in place of the full one. mpv
0.41 chooses the subtitle track from `--slang` and the `--subs-*`
options that `media-operator` writes, and its rules cause three
faults:

* `--subs-fallback-forced` is `yes` by default, so when a forced
  track is in the language of the audio, mpv takes it over a full
  track in the same language.
* mpv ranks a track that is flagged default above a full track, so a
  forced track with the default flag wins even with
  `--subs-fallback-forced=no`.
* In `auto` mode, `--subs-with-matching-audio=no` refuses every track
  when the audio is the first choice, so the forced track never
  shows. That is the one case where a forced track is the right one.

The flags and titles of the tracks are not reliable either. A survey
of a home cluster's catalog on 2026-11-08 counted 55,394 embedded
subtitle tracks. 166 tracks have "forced" in the title and no forced
flag, and 202 have the flag and no such title. 434 forced tracks also
carry the default flag, and 147 files hold one of those beside a full
English track. A probe of 7 of those files read mkvmerge's statistics
tags: forced tracks held 4 to 53 events, and full tracks 382 to 1,400.
One file held a forced track with no flag, no title, and the default
flag, and only its 22 events marked it. Two files held an English
track with 0 events. 3,506 external `.srt` files sit beside the
videos, named the way Bazarr names them (`.en.srt`, `.en.hi.srt`,
`.eng.forced.srt`). mpv reads the language and a `forced`, `sdh`,
`hi`, or `cc` suffix from such a name. With its default
`--sub-auto=exact`, mpv does not load the 543 files whose name ends in
a number, as in `.eng.0.srt`. Each of the 374 videos beside them has
another English track, so the design leaves that default as it is.

## The design

A forced track translates the parts of the audio that are not in the
audio's own language, so it belongs to the audio track. A full track
belongs to the reader. So `media-operator` chooses the subtitle track
itself, by role, and `library-operator` gives it the one fact that mpv
cannot read: how many events each track holds.

**The roles.** Each subtitle track takes one role, from its flags, its
title, the name of its file, and its event count:

* `empty`: the track holds 0 events.
* `forced`: the forced flag, a title with "forc" in it, a file name
  with a `forced` segment, or fewer events than a fifth of the largest
  track in the same language.
* `commentary`: a title with "commentary" in it.
* `sdh`: the hearing-impaired flag, or a title or file name segment
  that reads SDH, CC, or HI.
* `full`: every other track.

**The rule.** With `subtitles: on`, the command sidecar takes the
first full or SDH track in the order of `subtitleLanguages`. When
none exists, it takes the forced track in the language of the audio
that plays, if that language is one of `audioLanguages`. After that,
it takes a forced track in a language of `subtitleLanguages` under
audio in another language, then a full or SDH track that names no
language, and then a commentary track in the order of
`subtitleLanguages`, because commentary text follows the commentary
audio and not the film. A dry run of the rule over the catalog in the
survey found 50 videos that would show no subtitles without the two
middle steps: tracks with no language tag, and an English track
flagged forced under Korean audio.
With `subtitles: auto`, when the audio is the first choice, it takes
only that forced track, or none, and otherwise it takes a track as
`on` does. An empty track is never a choice. With no
`subtitleLanguages`, the rule reads in the order of `audioLanguages`,
and with neither list, the sidecar leaves mpv's own choice.
`subtitles: off` does not change. The sidecar chooses once for each
file, so a person who changes the audio by hand during a play chooses
the subtitles by hand too.

**SDH or full.** `sdh: prefer` or `sdh: avoid`, on the three tiers
that state the language preferences, puts SDH tracks ahead of full
tracks in the same language or behind them. With no preference, the
two roles rank the same.

**Where each fact comes from.** The probe already reads every video's
streams and every text subtitle file beside it. It records the
`NUMBER_OF_FRAMES` statistics tag of each embedded subtitle stream,
which ffprobe reads from the header, and the cue count of each text
subtitle file. A stream with no such tag has no count, and no count
differs from zero. The media browser puts the counts on each item's
`presentation.subtitles`, keyed by the stream's index in the container
or by the external file's name. The command sidecar applies the rule
when mpv has loaded the file and chosen the audio. It reads
`track-list`, which holds each track's flags, title, language, file
name, and container index, and sets `sid`. A Play with no counts, such
as one that `kubectl` creates, gets the same rule from the flags,
titles, and file names alone.

**mpv's own choice.** mpv chooses a track before the sidecar sets
`sid`, and its choice stands when the sidecar fails. So in `on` the
pod adds `--subs-fallback-forced=no`, and in `auto` it uses
`--subs-with-matching-audio=forced`. `--reset-on-next-file=sid` makes
mpv apply its own rule to each new file, in place of the `sid` that
the sidecar set for the item before.

**The backfill.** A probe record written before the counts holds no
`counted` field. The walk reads such a record of a video with a
subtitle stream, or of a text subtitle file, as a probe to do again,
so each of those files is probed once more. The probe reads the
headers of a video and at most 8 MiB of a subtitle file.

## Rejected

* **mpv's options alone.** `--subs-fallback-forced=no` and
  `--subs-with-matching-audio=forced` fix most files, but not a forced
  track flagged default or a forced track with no marker.
* **A count of the packets.** ffprobe's `-count_packets` reads the
  whole video over the network. A file without statistics tags falls
  back to its flags and title.
* **A language read from the title of an untagged track.** One video
  in the survey titled its untagged tracks "English", "English(SDH)",
  and "Spanish". Reading the names from the display tables of
  `golang.org/x/text/language/display` made the `media-operator`
  binary 2.3 MB larger, from 30.9 MB to 33.3 MB on 2026-11-08, and the
  untagged fallback already chose that video's English SDH track.
* **Subtitle downloads in `library-operator`.** Bazarr already writes
  the external files. Choosing among the tracks a file has is a
  separate problem from finding more.
