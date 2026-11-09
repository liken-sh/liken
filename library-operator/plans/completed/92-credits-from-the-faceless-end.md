# 92, Credits from the faceless end

This plan, written on 2026-11-09, adds a third tier to the credits
estimate of [plan 84](84-a-larger-up-next-card.md): the start of the
final run of a film's detections record in which the detector of
[plan 75](75-scene-level-cast-appearances.md) found no face. Built on
2026-11-09. No drill on `liken-1` has run.

## The problem

The up-next card rises at the credits, and both watched rules count a
play as finished there. A film with no credits mark falls back to the
rule of time. On a home library of 1,527 films on 2026-11-09, 849
films had no credits mark of the exact tier or the inferred tier, and
845 of them had a detections record from the `appearances` worker. That
record holds one sample a second with the faces in it. End credits are
text, so the faces stop where the credits start. In one film, every
30-second window held a face in each sample until 7,020 s, then no
window held one from 7,050 s to 7,470 s, and TheIntroDB put the credits
at 7,039 s.

## The design

- **The worker reads the run.** The detections record holds a vector
  for every face and runs to megabytes for a feature, so the walk never
  reads it. After the match, the appearances worker reads the record
  once and records the final faceless run of the one-second samples in
  its ledger entry, as `faceless`, from the run's first sample to the
  record's last sample. A record whose last sample holds a face records
  an empty run. A keyframe sample is left out, because the measurement
  below read the one-second samples alone.
- **The walk copies it.** The walk reads `faceless` from the
  appearances ledger into the new `files.faceless_tail_ms` column, where
  the answer's size is the file's size and the run is not empty.
- **The estimate reads it last.** The close container's estimate
  writes a credits row with the source `faces` only where a film has
  neither an exact clue nor an inferred one. The run must start in the
  last 15% of the film and last at least 120 s, and a film whose genres
  include `Animation` never takes it, because the detector finds few
  drawn faces. The row starts 90 s after the run starts, which
  [the early tail](#the-early-tail) chose. The row has the estimate's
  ordinal, so a better clue that lands later replaces it in place.
- **Old answers are read again.** An answer written before the worker
  read the run holds no `faceless`. The walk reads its found attempt as
  one whose output is missing, the rule that `uncountedProbe` follows
  for the probe, so the appearances gap opens. The worker finds the
  current detections record, runs only the match, and writes the entry
  with the run. That costs one match run for each video with an answer,
  each a pod of the worker `Job`, and no decode. On the library above
  that is more than 1,500 films, and every episode with an answer.

An episode takes no estimate of this tier. Its credits run under a
minute, the tier was not measured on episodes, and the siblings of the
inferred tier cover most episodes.

## The measurement

On 2026-11-09, read-only, over the home library above and its catalog.
For each non-animated film with a detections record, the rule above,
without the 90 s that [the early tail](#the-early-tail) added later the
same day, ran on the record. Its start was compared with the film's better
credits start: the earliest exact clue in the second half, or for a
film with none, the inferred estimate. This is one library on one day,
not a guarantee.

| Compared with | Films | Fired | Median error | Median \|error\| | 90th percentile \|error\| | Within 30 s | Within 120 s | Over 120 s early | Over 120 s late |
|---|---|---|---|---|---|---|---|---|---|
| an exact clue | 380 | 302, 79% | 6 s early | 17 s | 149 s | 69% | 86% | 5 | 37 |
| an inferred clue | 256 | 198, 77% | 82 s late | 115 s | 324 s | 24% | 53% | 14 | 79 |

Against the exact clues, the faces start was closer than the rule of
time in 85% of the films. It started after the point where the rule of
time raises the card in 18%, and after the watched line of the rule of
time, where the exact clue was before that line, in 24 films of 302,
8%. The late errors are films whose credits open over scenes with
actors in them, so the faceless run starts at the scrolling crawl. The
early errors are a story that ends on a long faceless shot. The error
against the inferred clues is larger in both directions, and the
inferred clue, the end of the last line of a subtitle file, is itself
an estimate, so that row bounds the agreement and not the error.

Of the 785 non-animated films with no better mark and a record, the
rule fired on 589, 75%.

The limits came from the same data. A shortest run of 30 s, 60 s, 90 s,
120 s, and 180 s moved the share within 120 s of an exact clue from 83%
to 87%, and the share fired on films with no mark from 83% to 69%. 120 s
kept 75% fired and cut the films whose mark is later than the rule of
time from 22% at 60 s to 18%. A window of 10% or 20% in place of 15%
changed no share by more than 1 point. Letting a run span one face
sample, or counting only faces taller than 5% of the frame, changed no
share by more than 2 points, so the rule counts every face.

## The early tail

The maintainer judged on 2026-11-09 that a card that rises late is
acceptable and one that rises early is not, because an early card covers
the end of the story. So the rule was measured again the same day, on
the same library, for its early errors against the 302 films with an
exact clue where it fired. 100 of them started more than 15 s early, 36
more than 30 s, 15 more than 60 s, and 5 more than 120 s.

The 36 films more than 30 s early have one shape. A large face of the
film's cast is on screen 1 to 5 s before the run starts, and then the
story ends on a shot with no face, such as a landscape, a ship that
leaves, or a title card, for 30 to 100 s before the provider's credits
start. None of them holds a face in a keyframe inside the run, and none
holds tiny faces before the run: the faces before the run are large.
The 5 films more than 120 s early have a provider's credits start only
58 to 117 s before the end against a faceless run of 300 to 480 s, so
the provider may mark a later part of the credits.

| Rule | Exact films fired | Early > 30 s | Early > 120 s | Fired on films with no mark | Median error | Median late error | Within 30 s | Within 120 s |
|---|---|---|---|---|---|---|---|---|
| shipped first: unbroken run to the end, start in last 15%, at least 120 s | 302 | 36 (11.9%) | 5 (1.7%) | 589 (75.0%) | -6 s | 72 s | 69% | 86% |
| a face in a keyframe ends the run | 306 | 38 (12.4%) | 5 (1.6%) | 605 (77.1%) | -6 s | 75 s | 69% | 86% |
| later of the run and the last named cast face | 302 | 36 (11.9%) | 5 (1.7%) | 589 (75.0%) | -6 s | 72 s | 69% | 86% |
| start in last 10% | 297 | 36 (12.1%) | 5 (1.7%) | 576 (73.4%) | -6 s | 71 s | 69% | 86% |
| start in last 8% | 282 | 35 (12.4%) | 5 (1.8%) | 549 (69.9%) | -6 s | 74 s | 68% | 85% |
| start in last 6% | 211 | 18 (8.5%) | 3 (1.4%) | 400 (51.0%) | -4 s | 74 s | 68% | 84% |
| run at most 480 s | 252 | 30 (11.9%) | 3 (1.2%) | 502 (63.9%) | -5 s | 71 s | 67% | 86% |
| run at most 360 s | 173 | 13 (7.5%) | 1 (0.6%) | 339 (43.2%) | -4 s | 68 s | 70% | 86% |
| 30 s later | 302 | 15 (5.0%) | 3 (1.0%) | 589 (75.0%) | +24 s | 26 s | 65% | 85% |
| 45 s later | 302 | 10 (3.3%) | 3 (1.0%) | 589 (75.0%) | +39 s | 40 s | 30% | 85% |
| 60 s later | 302 | 8 (2.6%) | 3 (1.0%) | 589 (75.0%) | +54 s | 55 s | 9% | 82% |
| 75 s later | 302 | 7 (2.3%) | 2 (0.7%) | 589 (75.0%) | +69 s | 69 s | 4% | 81% |
| **90 s later** | 302 | 5 (1.7%) | 2 (0.7%) | 589 (75.0%) | +84 s | 84 s | 3% | 80% |
| 120 s later | 302 | 3 (1.0%) | 2 (0.7%) | 589 (75.0%) | +114 s | 114 s | 2% | 69% |
| 60 s later, start in last 8% | 282 | 8 (2.8%) | 3 (1.1%) | 549 (69.9%) | +54 s | 56 s | 10% | 81% |
| 60 s later, run at most 480 s | 252 | 6 (2.4%) | 3 (1.2%) | 502 (63.9%) | +55 s | 56 s | 10% | 81% |

The median late error is the median of the films that started late.
The run already runs unbroken to the end of the record, so that
candidate is the first row. A named cast face is a face, so the last one
is never inside the run, and that row equals the first. Counting the
keyframes made the early tail slightly worse. A narrower window or a cap
on the run's length removed early films only by firing on fewer films.

The rule starts the estimate 90 s into the run. It reached 5 films of
302 more than 30 s early, 1.7%, and fires on the same films as before,
so it costs no coverage. The cost is lateness: against the exact clues
its median error is 84 s late, 90% of its errors are within 237 s, 80%
are within 120 s where 86% were, and 57 films start more than 120 s
late where 37 did.

## How the work is proved

Table tests cover the rule's limits, the genre, the tiers above it, an
episode, the read of the run from a record, the ledger entry the worker
writes, the walk's copy and its size check, the reopening of an answer
with no run, and the close container's derivation from the catalog,
including a chapter that lands later and replaces the estimate.

The drill on `liken-1` has not run. It plays a film whose only credits
mark has the source `faces` and checks that the card rises at it.
