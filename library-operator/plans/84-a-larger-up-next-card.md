# 84, A larger up-next card

This plan, written on 2026-10-27, proposes three changes to the up-next
card that a `Player` draws near the end of a work. It is a stub: it
states the problems and the shape of the contract, and leaves the open
questions for the design. [Plan 53](completed/53-up-next.md) built the
`next` block that the browser writes, and
[media-operator plan 28](https://github.com/liken-sh/media-operator/blob/main/plans/completed/28-up-next.md)
built the card that `upnext.lua` draws from it.
[Plan 62](completed/62-intro-and-credits-marks.md) moved the card's
rise to the start of the credits where the catalog has credits marks.
[Root plan 85](../../plans/85-motion.md) adds the compositor motion that
the redesign of the card uses, so the film can step back at the credits.

## The problems

**The card can rise at the wrong time.** Watching on a home cluster
shows that the card still rises too early or too late on some works.
No case is recorded yet. The design must first find which rule raised
the card on each such work: the credits mark of plan 62, the scene
after the credits, or the time rule for a work with no marks.

**The card does not say how long the next work runs, or when it
ends.** A person decides whether to start another episode or film
partly by the time. The card shows the art, `reason`, `title`, and
`detail`. An episode's `detail` holds its runtime as text, such as
"45 min", and a film's may not. Nothing on the card says when the next
work would end if it started now.

**The card stays on the screen after a person selects it.** A select
on the card dims it and its last line reads "Starting". The film plays
on under the card until the operator ends the `Play`. When the old
`Play` ends, the card stays over the loading screen until the next
`Play` draws. A person reads that as a card that did not take the
press.

## The contract

**The timing.** Record the works where the card rose at the wrong
time, with their credits marks and the rule that applied, and correct
the rule that fails. The rule lives in `media-operator`'s display, and
the marks come from this operator's catalog, so the fix can land in
either component.

**The runtime and the end time.** The browser adds the next work's
runtime to the `next` block as a number, such as `runtime_seconds`,
from the probed duration in the catalog. `media-operator` copies it to
the display. The display makes the card larger, so that it has room
for a line with the runtime and the end time. The display calculates
the end time from the wall clock when it draws the card: the time that
remains in the current work plus the runtime of the next work. The
display draws it in the clock format of the screen.

**The dismissal.** A select on the card removes the card at once, or
replaces it with a loading state that the next `Play`'s screen
continues. The display must still route every press except back to
nothing until the next `Play` starts, as it does now.

## Open questions

* Which works raise the card at the wrong time, and which rule raised
  it on each.
* Whether the end time counts from the press or from the end of the
  current work. A press starts the next work at once, so the press is
  the likely answer.
* Whether a film's `detail` should drop its runtime text when the card
  draws the runtime from the number.
* Whether the dismissed card should leave a loading mark on the screen
  until the next `Play` draws, and which component draws it.

## How the work is proved

The display's tests cover the card's size, the runtime line, the end
time against a fixed clock, and the dismissal on select. The browser's
tests cover the runtime in the `next` block for an episode, a franchise
member, and a film of a set. A drill on `liken-1` plays an episode to
its credits, checks the time of the rise and the lines on the card,
selects the card, and confirms that the card leaves the screen before
the next episode draws.
