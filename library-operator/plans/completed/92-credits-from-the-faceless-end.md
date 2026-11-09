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
  drawn faces. The row has the estimate's ordinal, so a better clue that
  lands later replaces it in place.
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
For each non-animated film with a detections record, the rule above
ran on the record. Its start was compared with the film's better
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

## How the work is proved

Table tests cover the rule's limits, the genre, the tiers above it, an
episode, the read of the run from a record, the ledger entry the worker
writes, the walk's copy and its size check, the reopening of an answer
with no run, and the close container's derivation from the catalog,
including a chapter that lands later and replaces the estimate.

The drill on `liken-1` has not run. It plays a film whose only credits
mark has the source `faces` and checks that the card rises at it.
