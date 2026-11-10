# Appearances are not in the catalog

The `appearances` fact writes, for each video, a ledger of who is on
screen at each keyframe and a spans file of the runs that name the
same people. The pause row on the film's display reads the spans file
named in the `Play`, so it needs nothing from the catalog. Two other
features from plan 75 need a query across titles: the scenes of one
person from that person's page, and a jump to the next appearance of a
person.

Plan 75's step 2 designed the table those features read. The walk
reads each video's `.liken/appearances/<video>.matches.json` into a
catalog table with one row per observation, deletes the rows with the
file, and sweeps them the way it sweeps the `marks` table. The walk
reads only the counts from the ledger, and it never reads the faces
record. Neither the table nor either feature is built. The design is
in [plan 75](../completed/75-scene-level-cast-appearances.md#step-2-the-catalog).

## Rows of observations or rows of spans

A film had about 650 to 780 named faces in plan 75's measurements, one
per keyframe. One row per observation is then about one million rows
for a library of about 1,450 films, and every copy of the catalog
carries them: the catalog pods', each library `Job`'s, and each
screen's. On a home cluster on 2026-10-07, each node's pod-storage
partition was 8.3 GB with 4.4 to 7.1 GB free, and a copy of the catalog
was about 720 MB before any appearance row. A span joins the keyframes
of one person into one interval, which is several times fewer rows,
and the scenes of a person and a jump to the next appearance both read
intervals. The spans file already holds them. Rows of observations keep
each face's similarity for a later threshold, but the ledger on the
volume keeps it too. Rows of spans were the recommendation when plan 78
set this work aside.

## A panel of actions on a cast card

On 2026-11-09 the film's display opened a panel when a person pressed
`select` on a focused card of the "In this scene" row, and the same
day it was removed, so `select` on a card plays or pauses the film
again. The panel had a "Next scene" row, and an "Open <person>" row
was designed for it but never landed. "Next scene" read only the spans
file, so it needed nothing from the catalog. It sought to the start of
that person's next scene by the rule the cast row uses: a gap of up to
10 seconds between two spans does not end a scene. "Open <person>"
needs a message from the player to the media browser that opens that
person's page, and no plan has designed that message.
