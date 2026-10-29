# 84, A larger up-next card

This plan, written on 2026-10-27, proposes three changes to the up-next
card that a `Player` draws near the end of a work. It is a stub: it
states the problems and the shape of the contract, and leaves the open
questions for the design. [Plan 53](53-up-next.md) built the
`next` block that the browser writes, and
[media-operator plan 28](../../../media-operator/plans/completed/28-up-next.md)
built the card, which `media-operator/display/src/upnext.rs` draws
from it.
[Plan 62](62-intro-and-credits-marks.md) moved the card's
rise to the start of the credits where the catalog has credits marks.
[Root plan 85](../../../plans/completed/85-motion.md) adds the compositor motion that
the redesign of the card uses, so the film can step back at the credits.

Status on 2026-10-28: the dismissal is built.
[media-operator plan 44](../../../media-operator/plans/completed/44-the-display-arranges-its-claim.md)
(d3287df8) made a select on the card fade the film and the card's
column out over 300 ms and pause mpv, and the display draws nothing
once mpv's socket closes, so the browser's loading curtain shows until
the next `Play` draws.

Status later on 2026-10-28: the timing, the runtime, and the end time
are built, in the three commits that "The decisions" names, and the
drill on `liken-1` in "How the work is proved" passed. The plan is
complete. No case of a card that rose at the wrong time was recorded
before the estimate was built, so none is checked against it.

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

## The decisions

These decisions were made with the maintainer on 2026-10-28, and they
answer the open questions that the stub listed.

**The timing uses every clue, and errs earlier.** No case of a late or
early rise was recorded. The maintainer judged that an early rise costs
little now that the film steps back at the rise, and that a late rise
is the larger annoyance. So `library-operator` derives one credits
start and one intro for each main video from every clue it has, and
writes them as marks with the source `estimate` beside the providers'
marks (07f43beb). The estimate runs in the close container of every
library Job. The clues are in two tiers. The exact tier is the
providers' marks, a chapter whose title names the credits or the intro,
and the start of a `preview` span. The inferred tier is the median
credits length or intro of the series' other episodes, and the stretches
with no dialogue in a text subtitle file beside the video. The inferred
tier counts only where the exact tier has nothing, and the earliest clue
in a tier wins. The display, the skip control, and both watched rules
read credits and intro marks of any source, so the card and the watched
line still agree. The estimate is computed in `library-operator`, and
not in the display, for that reason: an earlier rise that the watched
rule did not know would leave the work the person just finished in the
continue-watching row.

**The time rule moves earlier, inside the watched line.** A work with no
mark of any kind raises the card at the smaller of 4% or 240 s before
its end for a film, and the smaller of 2.5% or 120 s for an episode
(4cb45f8e). Both amounts stay under the watched rule's 5% and 300 s, so
the card never rises before the work counts as watched, and the watched
rule did not change.

**The end time counts from now.** A select on the card starts the next
work at once, so the time that matters is when the next work ends if it
starts now. The browser adds `remaining_seconds` to the `next` block:
the next work's duration less the position a play of it resumes from.
`library-operator` copies it to the `Play` as `remainingSeconds`, and
the display draws "Ends at 10:42 pm" under the detail line, on the card
and on the stepped-back column, in the screen's clock format
(4cb45f8e, f07dd24b). The line changes when the wall minute turns.

**The detail line keeps its runtime.** The detail line already held the
runtime as text for an episode and for a film. It stays, and the new line
adds only the end time.

## How the work is proved

The display's tests cover the card's size, the runtime line, the end
time against a fixed clock, and the dismissal on select. The browser's
tests cover the runtime in the `next` block for an episode, a franchise
member, and a film of a set. A drill on `liken-1` plays an episode to
its credits, checks the time of the rise and the lines on the card,
selects the card, and confirms that the card leaves the screen before
the next episode draws.

The drill ran on `liken-1` on 2026-10-28, on the development build
`2026.10.28-005-dev-004-b6ecdf15` of both operators. Corrosion altered
the replicated `files` table for the four new columns in 14 ms when the
catalog pod started, with no error. One walk of the `series` library,
which took 17 minutes, wrote 2123 credits marks and 489 intro marks
with the source `estimate`. Before the walk, 4429 files of that library
had a provider's credits mark. After it, 6408 files had a credits mark
of some source. The probe read 3102 subtitle files. It found no
chapters, because the drill did not probe the library again, and a
probe of the whole library from `liken-1` would write the probe
records on volumes that another cluster shares. The `Play` of an
episode of a series with no provider's credits mark carried the
estimated credits start at 2658.4 s of 2706.6 s, which the median of
its season's other episodes set, and `remainingSeconds: 2712` for the
next episode. A seek to about 2642 s raised the card about 20 s later,
at the estimated start, and not at 2645 s, where the time rule would
have raised it. The stepped-back column drew "Ends at 11:43 pm", which
is the wall clock plus the 2712 s. The chapter clue is proved by the
tests only.
