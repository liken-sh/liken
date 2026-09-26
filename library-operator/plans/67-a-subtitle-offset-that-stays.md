# 67, A subtitle offset that stays with the media

This is a stub for later. Nothing here is built. When a person nudges the
subtitle offset on the screen, the offset is saved beside the library's
files, and the next play of the same video with the same subtitle track
starts with that offset.

## The problem

Some subtitle tracks are early or late against the video. The control
strip on the media-operator screen has a subtitle offset that moves
mpv's `sub-delay` by 50 ms for each press, so a person can correct the
timing by hand. The correction is lost when the play ends. The next play
of the same film starts at 0, and the person must find the same offset
again.

The strip also does not say which direction is which. Right makes the
offset larger and left makes it smaller, but a person who sees "+250 ms"
cannot tell whether the subtitles now show sooner or later.

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
and the value carries the same word, for example "+250 ms later". That
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
- The adjuster clamps the offset to 5 seconds in each direction.

## Not in this plan

The audio offset is not saved. It corrects the latency of the room's
receiver and television, not the media, so it does not belong in the
library.

An automatic sync of a subtitle to the audio is a separate later plan,
named in [plan 60](60-the-subtitle-fact.md).
