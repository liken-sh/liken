# The track labels say too little and too much

Open problem. The display that a playback pod draws over the film
names each track with `Track::label` (`display/src/film.rs`): the
track's title and its language, as `track-list` carries them. That
label is too long for the control strip when a file gives a long
title, and too short in the subtitle chooser when a file gives no
title. Both were seen on a home cluster's television with a Blu-ray
rip.

## A long title runs over the strip

The strip draws its groups at a fixed pitch of 430 canvas pixels
(`GROUP_PITCH` in `display/src/strip.rs`), and it draws a track value
with no limit on its width. An audio track titled "Compatibility
Track / Dolby Digital Audio / 5.1 / 48 kHz / 640 kbps" draws across the
audio offset, the `subtitles` heading, and the subtitle value. The
chooser does not have this fault: `display/src/chooser.rs` clips a row
that is wider than the panel and ends it in an ellipsis, so a long
commentary title reads correctly there.

The fix is probably to clip each strip value to its group's width in
the same way. Undecided: whether the audio cell should read only the
language, as the subtitle cell does, and leave the full title to the
chooser.

## A subtitle list reads only language codes

`Track::Subtitles.value` draws the language alone in the strip cell,
by design. The chooser draws `Track::label`, which is the title and
the language. Many files give their subtitle tracks a language and no
title, so every row of the chooser reads `ENG`, `ENG`, `SPA`, and a
viewer cannot tell a forced track from a full one, or SDH from plain.

The display parses five fields of each `track-list` entry: `id`,
`type`, `title`, `lang`, and `selected`. mpv 0.41, which the `mpv` base
image holds, carries more, and these fields could tell two tracks of
one language apart:

- `forced` and `default`, the container's flags.
- `hearing-impaired` and `visual-impaired`, the flags for SDH and
  described tracks.
- `external` and `external-filename`, for a sidecar file such as
  `Movie.en.forced.srt`. The file name often holds the only
  description of the track.
- `codec`, which tells a bitmap track (`hdmv_pgs_subtitle`,
  `dvd_subtitle`) from a text track (`subrip`, `ass`).

The display could also name the language in full ("English" for
`eng`). mpv gives only the code, so that needs a table of ISO 639
codes in the display.

Check each field against the `track-list` of a real file before the
design depends on it. Which flags a rip sets depends on the tool that
made it.
