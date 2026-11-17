# Only TMDb gives the names of the whole crew

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
`billing.yaml` lose. On the videos that plan 96 checked, the names of
the whole crew changed the placement of 16 of 436 by more than 10
seconds.
