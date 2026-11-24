# 67, A subtitle offset that stays with the media

This is a stub for later. When a person nudges the subtitle offset on
the screen, the offset is saved beside the library's files, and the
next play of the same video with the same subtitle track starts with
that offset.

Status on 2026-11-23: the section "The strip names the direction" is
built, by
[`media-operator` plan 48](../../media-operator/plans/completed/48-one-panel-for-a-track-and-its-timing.md).
The words differ from what the section proposed. The panel has no hint
for the keys. Its readout names what moves and the direction, such as
"Subtitles 0.3 s later", and "Subtitles as timed in the file" at zero,
and the ends of its scale read "sooner" and "later". The strip's value
ends with the same words, as in "English · 0.3 s later". Nothing else
in this plan is built.

## The problem

Some subtitle tracks are early or late against the video. The subtitle
panel of the control strip on the media-operator screen moves mpv's
`sub-delay` by 100 ms for each press of left or right, so a person can
correct the timing by hand. The correction is lost when the play ends.
The next play of the same film starts at 0, and the person must find
the same offset again.

When this plan was written, the strip also did not say which direction
was which: a person who saw "+300 ms" could not tell whether the
subtitles showed sooner or later.

## The idea

**The offset belongs to the media.** An offset corrects the timing of
one subtitle track against one video file. It is the same for every
person who watches, so it is not keyed by person. It is a fact about the
media, and it is saved next to the library's files with the other facts
this operator records there.

**The key is the video file and the track together.** The track is an
external subtitle file, such as `Movie (2001).en.srt`, or a stream
embedded in the video. The same `.srt` can need a different offset
against a different cut of the film, and a new file, such as a 4K
upgrade, has its own timing. So the offset is keyed by the pair, and a
new video file starts with no offset.

**The saved offset is the default for the next play.** When a play
selects a track that has a saved offset, the play starts with that
offset. The person can still nudge it, and the new value replaces the
saved one. A reset to 0 removes the saved offset.

**The screen reports intent, and library-operator records it.** The
change travels from the media-operator screen to library-operator over
the bus. media-operator reports what the person did, and it does not
read or write a catalog. library-operator owns where the offset is
stored.

**The strip names the direction.** A positive `sub-delay` shows the
subtitles later. The adjuster's hint changes to name the direction of
each key, for example "left: subtitles sooner, right: subtitles later",
and the value carries the same word, for example "+300 ms later". That
change is in media-operator's `display` crate.

## What the design must answer

These are the constraints found while the idea was discussed. The
design that builds this plan decides how to meet them.

- mpv has one `sub-delay` for the whole player, not one for each track.
  mpv selects the subtitle track itself, and a person can change tracks
  during a play. The offset must follow the track that plays.
- A Play can hold several items, such as the episodes of a season. The
  saved offsets apply to each item, and an item with no saved offset
  plays at 0.
- A play that does not change the offset must not change the saved
  value. A Play that another tool created, with no saved offsets in it,
  plays at 0, and that 0 must not erase the saved offset.
- A subtitle file that is renamed or removed leaves its saved offset
  with no track. The scanner's mark-and-sweep must remove it.
- The subtitle panel clamps the offset to 10 seconds in each direction.

## Not in this plan

The audio offset is not saved. It corrects the latency of the room's
receiver and television, not the media, so it does not belong in the
library.
