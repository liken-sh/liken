# An episode the model is sure of moves toward its season's usual length

Open bug. A unit test reproduced it on 2026-11-19.

`titles place` places each video alone, and then places it again with
the usual length of the end titles of its folder as a guide, when at
least three other videos of the folder were placed with a confidence of
0.8 or more (`titles/src/place.rs`). The guide is there for an episode
whose own reading is unclear. `place` applies it to every video of the
folder, also to one it was already sure of.

The placing `Job` places a whole library, so every episode of a season
is placed with its folder. On 2026-11-17, on a home cluster, the starts
of 88 episodes that a person had checked were compared both ways, with
the Library's own model:

| | Within 3 s | Within 10 s | Within 30 s |
|---|---|---|---|
| Each episode placed alone | 69 | 83 | 84 |
| Each episode placed with its folder | 62 | 82 | 84 |

The guide moved 20 of the 88 by more than a second, most of them 2 to
5 seconds toward the folder's usual length, and raised their confidence
from about 0.8 to about 0.95. One episode that the model was not sure
of moved from 3 seconds late to 29 seconds late.

The question is whether a video that `place` is sure of alone keeps its
own start, and takes the folder's guide only where it is not sure. The
88 episodes are few, and
[plan 96](../completed/96-end-titles-from-the-picture.md) records no
measurement of what the guide does for the episodes the model is not
sure of. So the change needs both groups measured.

## The reproduction

A test placed four episodes of one folder with a temperature of 8.
Three have end titles of 60 seconds, and the fourth has end titles of
64 seconds, with answers that are clear on each side of the start.
Placed alone, the fourth episode starts at 336 s with a confidence of
at least 0.8, which the test checks first. The test wanted the same
start when the folder is placed together, and failed with:

```
the episode starts at Some(340.0) with its season and at Some(336.0) alone, want its own start because the model was sure of it
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should an episode whose end titles the model places with confidence
on its own keep that start, and take the season's usual length only
where the model is not sure? Or should every episode of a season move
toward its usual length, as now?
