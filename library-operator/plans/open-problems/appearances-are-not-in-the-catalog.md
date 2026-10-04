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
