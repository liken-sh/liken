---
title: Targets
weight: 10
---

# Targets

A `Target` states what you want to image: an object, where it is on
the sky, and the frames you want of it. It lasts as long as the work
does, across as many nights as it takes, so you declare it once and
reserve the telescope as often as the sky allows. This page gives what
the fields mean, and [Imaging](/docs/concepts/imaging/) gives what the
operator does with them on a night. [`Target`](/docs/reference/targets/)
lists every field.

```yaml
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Target
metadata:
  name: m42
  namespace: observatory
spec:
  object: M42
  coordinates: {ra: "05:35:17.3", dec: "-05:23:28"}
  opticalTrain: east-imaging
  sequences:
    - {filter: Luminance, exposure: 300s, count: 60}
    - {filter: Red, exposure: 300s, count: 20, binning: 2}
  dither: {every: 2, pixels: 5}
  centering: {solver: lab, tolerance: 30}
  archive:
    claimName: archive
    path: astro
  fitsKeywords:
    - {name: PROGRAM, value: Orion widefield}
```

## Which reservations image a target

There is no session resource. A `Reservation` of
`observatory-operator` whose `spec.holder` is
`astrophotography-operator` images the targets of its own
namespace: the target with the highest `priority` first, then the
oldest. A target is eligible when `suspend` is not `true`, its
`Complete` condition is not `True`, a sequence has work left, no other
reservation images it, and its `opticalTrain` is one of the session's
trains. A target with
no `opticalTrain` can be imaged on any telescope whose session has
exactly one imaging camera. A target finds its reservations, so you
never edit a target for a new night.

The target, its archive, and the reservation's outbox are in one
namespace, because the operator's pods mount the claims, and a pod
mounts only the claims of its own namespace.

## Coordinates

`coordinates` are J2000, the form that SIMBAD and KStars print: the
right ascension in hours as `HH:MM:SS.s`, and the declination in
degrees as `±DD:MM:SS.s`. The API server refuses any other form, so a
right ascension in decimal hours never reads as degrees. An INDI mount
takes coordinates at the equinox of the date, which differ from J2000
by about 20 arcminutes for M42 in 2026. The operator converts them
before each slew.

## Sequences, and how frames count

Each sequence is a filter, an exposure, a count, and optionally the
binning, the gain, and the offset. A field you leave out leaves the
camera's own value, so a camera with no gain property works. The
operator exposes the first sequence that has work left until it has
none, and then the next, because a change of filter costs a refocus
on a real rig.

A frame counts toward a sequence by its settings: the same filter,
exposure, binning, gain, and offset. So you can reorder or extend the
sequences, and the frames that still match keep counting.
`status.sequences` shows `archived`, the frames in the archive's
catalog, and `pending`, the frames that wait in an outbox. The
operator writes it with zero counts, with `status.progress` and
`Complete`, as soon as it reads a new target, and again after each
edit of the sequences. The work
left is `count - archived - pending`, so a target can end with more
frames than it asked for when a promotion lags, and never with fewer.

## The archive and the file names

The camera writes each frame once, into the reservation's outbox
on the machine at the pier, under its final name. The operator then
moves it to `spec.archive`: a claim of yours, and a directory in it.
`archive.claimName` cannot change after you create the target,
because the counts come from the catalog in that claim. To use
another claim, create another target.

`archive.format` gives each frame's file name, with tokens such as
`{object}`, `{filter}`, `{night}`, and `{datetime}`, and
`archive.promotion` decides when frames move.
[The archive](/docs/concepts/archive/) explains both, and how a frame
moves with no loss.

## The frame catalog

The catalog is one file for each target, at
`<archive.path>/catalog/<Target>.jsonl` in the archive, with one line
of JSON for each frame. It stays beside the frames, so a backup of the
archive takes it along. [The archive](/docs/concepts/archive/#the-catalog)
lists the fields of a line.

## FITS keywords

Every frame carries the keywords of `observatory-operator`'s
levels, then the target's `fitsKeywords`, then two that this operator
writes: `OBJECT`, from `spec.object`, and `FRAMEID`, the frame's
identity, a UUIDv7. The API server refuses `OBJECT` and `FRAMEID` in
`fitsKeywords`, and every keyword that the camera's driver measures,
such as `EXPTIME`.
`observatory-operator`'s page on
[FITS keywords](https://liken.sh/observatory/docs/concepts/fits-keywords/)
explains the levels.

## The conditions

| Condition | `True` | `False` reasons |
|---|---|---|
| `Complete` | every sequence has at least its count archived | `WorkLeft` |
| `Imaging` | a reservation images the target; the reason is `Slewing`, `Centering`, `Guiding`, or `Exposing` | `Waiting`, `Suspended`, `SequencesDone`, `NoTrain`, `NoFilter`, `SlewFailed`, `CenteringFailed`, `GuidingFailed`, `OutboxFull`, `ExposureFailed` |
| `ArchiveAvailable` | the last promotion succeeded | `ClaimMissing`, `Unschedulable`, `CopyFailed`, `VerifyFailed`, `NameConflict` |
