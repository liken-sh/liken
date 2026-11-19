# Only TMDb gives the names of the whole crew

Open bug. A unit test reproduced it on 2026-11-19.

The `titles` tool looks for the names of a video's people in the text
of each frame, and a title sequence mostly shows the crew: the
producers, the composer, the cinematographer, the editor. So the
`credits` fact writes `.liken/billing.yaml` beside `credits.yaml`, with
every person the provider credits and each person's department and job
([plan 96](../completed/96-end-titles-from-the-picture.md#the-names-of-the-whole-crew)).

Only the TMDb source writes it. The IMDb datasets and TVmaze still pass
the cast, the directors, and the writers alone, so a library whose
`credits` source is one of them gives the tool fewer names. A title
that TMDb credited before the fact wrote `billing.yaml` has none until
the `credits` fact asks again, which naming `credits` in `spec.refresh`
starts.

Nobody has measured how much the placements of a library with no
`billing.yaml` lose. On the 436 videos that plan 96 checked, two changes
together, more names of the crew and a match across the letters of a
whole frame, changed the placement of 16 by more than 10 seconds. That
measurement used the key crew of IMDb's `title.principals` in place of
TMDb's crew.

## The reproduction

A test ran the `credits` fact from the IMDb datasets for a film whose
rows in `title.principals` include a producer, Paul Stone. The fact
wrote `credits.yaml`. The test wanted `billing.yaml` beside it with
the producer, and failed with:

```
reading billing.yaml after the IMDb credits: open …/Winter Harbour (2011)/.liken/billing.yaml: no such file or directory, want the file with the producer Paul Stone
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should the IMDb and TVmaze sources write `billing.yaml` too, and which
of the crew that they credit count: every principal of
`title.principals`, or only some categories?
