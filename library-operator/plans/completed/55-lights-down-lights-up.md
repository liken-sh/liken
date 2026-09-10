# 55, Lights down, lights up

Built, and drilled on `liken-1` on 2026-09-10 in release 2026.09.10-002,
which rolled to the house the same evening. The browser dims its whole
frame when a film is on its way and brightens it when the film is over,
in step with the curtain it already draws. This is library-operator's
part of the theater transition: display-operator's
[plan 18](https://github.com/liken-sh/display-operator/blob/main/plans/completed/18-a-surface-leaves-with-a-fade.md)
fades the film's surface in over the dimmed page and out again, and
media-operator's
[plan 30](https://github.com/liken-sh/media-operator/blob/main/plans/completed/30-the-film-leaves-with-the-lights.md)
holds the film for the fade out.

## The problem

A select on a film draws the loading curtain: the page departs and the
logo waits at full brightness until the film's surface covers it. With
the film fading in over the page for 600 ms, a page at full brightness
under a half-transparent film is a muddle. And when the film fades out
at the end, a page that snaps to full brightness under it is a light
switch, not a theater.

## The design

**Lights down.** From the second the `Player` status moves away from
`Idle`, the whole frame dims from full to a floor over
`look::LIGHTS_DOWN`, about 1.2 s, with the curtain's logo pulsing on
top at full brightness. The curtain is the select's, the lights are the
status's, and `Idle` ends both. The status is the one way the lights go
down, so a `Play` that `kubectl`, another client, or an automation
created dims the page the same way, with no curtain over it. The
dim is a scrim over the frame in the browser's own draw, so nothing in
the compositor changes and the dimmed page is what the film fades in
over. The scrim is the frame's own layer over whatever screen is
showing, so the home page, a wall, and a title's page all dim the same
way, and the curtain's logo draws over it. The floor is dark and not
black, so the page reads as a room with the lights down and not as a
dead screen. It is an eighth of full, and the renderer composites in
linear light, so an eighth reads as about a third of the sRGB value on
the panel.

**Lights up.** On the status's move to `Idle`, the scrim lifts over
`look::LIGHTS_UP`, about 0.3 s, on the same frames as the curtain's
exit, which already runs `look::RETURN` at 0.4 s. The film fades out
above over 250 ms, so the page is most of the way back up as the last
of the film goes.

**A `Play` that never plays** takes the same lights-up on its move to
`Idle`, so a failed start leaves no dim page.

The scrim is a state of the browser's frame, beside the curtain, and
the harness knows nothing of it. Nothing on the bus changes.

## How the work is proved

Built in 2186c97 and cfe2477, drilled from development builds
2026.09.10-001-dev-002 and -dev-003, and released in 2026.09.10-002,
after display-operator and media-operator 2026.09.10-001.
Frames captured on vega on a colored test backdrop read on-screen
levels of 0.77, 0.56, 0.35, and 0.18 at 0.2 s steps, and then the
0.127 floor, with the mark and the header at full brightness through
all of them. Lights down runs 1.2 s and lights up runs 0.3 s.

The drill decided two things the design above now states. The dim runs
from the `Player` status's move off `Idle` and from nothing else, not
from the select; a film started by `kubectl` on the portable panel
dimmed the page with no curtain over it and lifted it at the end. And
the scrim is a layer of the whole frame, so the home page and the walls
dim with a title's page.

The full sequence with the `theater` `Layout` (display-operator plan
18, media-operator plan 30) was seen by eye on the portable panel: the
logo comes in on the select, the page dims as the `Play` arrives, the
film fades in over it, and on the way out the film fades out while the
page comes back up and the logo goes out. With media-operator's latency
fix, the `Player`'s `Idle` reaches the browser 2 ms after the ending
report.

The plan as written:

1. `make test` in the operator and the browser, with the scrim's
   levels proved at the ends and the midpoint of both moves.
2. Frames captured on vega with the headless harness through a
   scripted select, a `Starting`, a `Playing`, and an `Idle`, showing
   the dim and the lift, for Chris to look at before it rolls.
3. On `liken-1` with the `theater` `Layout`, a film started from the
   browser and ended with the remote, seen by eye.
