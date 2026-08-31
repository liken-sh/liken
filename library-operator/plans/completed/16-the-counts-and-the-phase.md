# The counts and the phase

Plan 16. What `kubectl get libraries` says. At the end of this plan one
line per library says how many titles it holds, how many items are in
it, how many files the volume holds, and whether a walk is running right
now.

## The problem

A `Library` reports its titles and whether it is `Ready`. Neither
answers the questions a person asks first.

A title count and an item count are different numbers. A movies library
of 812 titles holds 812 movies, so the two agree. A series library of 47
titles holds 1204 episodes, and the titles column says 47. The catalog
holds both numbers and the status reports one.

A library's file count is nowhere. The volume holds the video files, and
after [plan 17](17-every-file-a-title-carries.md) it holds the `.nfo`
files, the art, the subtitles, and the trickplay directories as well.
The catalog counts them and nothing reports the count.

`Ready` says the scanner works. It does not say what the scanner is
doing. A walk of a large volume runs for minutes, and for those minutes
`Ready` reads `True` and the counts stand at the last walk's numbers. A
person who is waiting for a new title to appear has no way to tell a
walk in flight from a walk that has finished and found nothing.

## The three fields

The scanner's report gains three fields, and the `Library`'s status
carries all three.

`items` is how many item rows the catalog holds for this library, across
the movies, series, and episodes tables. The scanner already reads that
count twice per walk, once before the walk and once after it, for the
prune guard. The count after the prune is the one the report carries.

`files` is how many file rows the catalog holds for this library. It is
one more count query beside the one that reads `items`, read at the same
point in the walk.

Both counts are what the catalog holds, not what the walk found. The
walk's own count describes the volume; the catalog's count describes
what a screen can read. They agree after a clean walk, and after a walk
that skipped its prune the catalog's count is the honest one. A count
query that fails leaves the field at its last value, the way the walk
already keeps its last report.

`phase` is what the scanner is doing: `Scanning` while a walk is in
flight, and `Idle` between walks. The scanner publishes its report twice
per walk, once as the walk starts and once as it ends, so the bus
carries the change and the operator writes it through. The report is
retained, so a subscriber that arrives during a walk reads `Scanning`.

## The phase the operator writes

`status.phase` is a function of the same facts the `Ready` condition
reads, so the two can never disagree. The operator derives it in the one
place every status is derived, and it takes the first of these that
holds.

* `Pending`, where `Ready` is not `True`. The storage is not bound, the
  namespace holds no single `Catalog`, there is no scanner pod, the pod
  has not started, or no report has arrived yet. The `Ready` condition's
  reason says which.
* `Offline`, where the bus says the scanner left. The counts still
  stand, because they describe the volume and not the pod that walked
  it, and the phase says no scanner is there to walk it again.
* `Scanning`, where the newest report says a walk is in flight.
* `Idle`, otherwise.

So the phase never reads `Idle` or `Scanning` for a library that is not
`Ready`, and a person reads one column instead of two.

## The columns

The printer columns become the seven a person reads at a glance, and the
two that answer a second question move behind `-o wide`.

```
NAME     KIND     TITLES  ITEMS  FILES  STATUS    READY  AGE
movies   movies      812    812   3980  Idle      True   6d
shows    series       47   1204   6621  Scanning  True   6d
```

`Claim` and `Unidentified` take `priority: 1`, which is how a
`CustomResourceDefinition` says "show this under `-o wide` and not
before". Both stay in the status and in the generated reference, and
neither is dropped.

## What was considered and set aside

**A column per kind.** A `titles` column beside an `episodes` column,
and a `tracks` column when music arrives. Every kind would read zero in
every other kind's column, and the printer would grow one column per
kind for as long as the project adds kinds. One `items` column counts
whatever a kind puts in the catalog, and the kind column says what those
items are.

**Counting during the walk.** The walk counts the rows it wrote, and
that number is free. It is also the wrong number. It describes the
volume as the walk read it, before the prune removed what left the
volume, and a walk that skipped its prune would report a count the
catalog does not hold. The catalog's own count is what a screen reads,
so the catalog's own count is what the status reports.

**A `Scanning` condition.** A condition reports what a boot or a pass
actuated, and it stays true or false until the next pass. A walk is a
state that changes several times an hour. That is a phase, which is the
word Kubernetes already uses for it on a `Pod`.

## The proof

The unit tests cover the derivation: each of the four phases from its
facts, the counts carried through from a report, and the count query
that fails leaving the last value in place.

The drill runs on `liken-1`. Roll the operator, watch `kubectl get
libraries` through a full walk, and record the line the printer shows
before, during, and after. Confirm the `Items` count matches the
catalog's own count for a series library, and that the `Files` count
matches the file rows after [plan 17](17-every-file-a-title-carries.md).
Delete the scanner pod and confirm the phase reads `Pending`, then
`Scanning`, then `Idle`.
