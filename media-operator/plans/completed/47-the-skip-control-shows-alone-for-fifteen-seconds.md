# 47, The skip control shows alone for fifteen seconds

Proposed and built on 2026-11-22. The display's tests pass. The drill
on `liken-1` has not run.

The window is 10 seconds, not the 15 that this plan describes. The
maintainer watched the 15-second control on a television the same day
and found it a little too long.

## The problem

While the playhead is inside an intro or a recap, the display shows
"Skip intro" or "Skip recap" over the bare video for the whole span.
A recap can run for two minutes, and the control stays on the film
for all of it, whether the viewer wants it or not. The up-next card
already works the other way: it appears on its own, hides after five
seconds, and comes back with the OSD.

## The design

The intro and recap control shows over the bare video only while the
playhead is in the first 15 seconds of its span. After that, the
control is still a stop of the OSD for the rest of the span, so a
press that shows the OSD shows the control again, and the control
hides when the OSD hides.

The window follows the playhead and not a wall-clock timer. A viewer
who pauses at the start of the intro still gets the control when the
film resumes, and a seek back to the start of the span shows it again.
A seek into the middle of the span shows it only on the OSD.

`Skip::on_position` takes the playhead's position with the span that
the marks offer. It shows the control's own fade while the position is
before `span.start + 15`, and hides that fade after. A select over the
bare video skips only while the control shows there. After the window,
a select over the bare video pauses, as it does with no control.

Focus is unchanged. A press of up or down on the hidden OSD shows it
with focus on the control, because `Focus::summon` focuses the first
stop below the cast row and the up-next offer, and the control is that
stop while the playhead is inside the span. So the select after the
press skips.

The post-credits control keeps its whole span. The credits run for
minutes with the up-next card beside the control, and the control
draws unfocused there, so it takes no select over the bare video.

## What was considered

- **A hide timer of 15 seconds, as the up-next card has.** The frame
  loop owns the timers. A timer counts the time a film is paused, so a
  pause at the start of the intro would use up the window, and a seek
  back into the span would need the timer armed again. The position
  already arrives on every `time-pos` report, so it needs no timer.

## How the work is proved

The tests in `display/src/skip.rs` and `display/src/focus.rs` cover
the window, a seek past it and back, the post-credits control, and the
press of up or down after the window. The drill plays an episode with
a marked intro on `liken-1` and captures the display at the start of
the intro, at 15 seconds into it, after the window with the OSD up,
and after the OSD hides.
