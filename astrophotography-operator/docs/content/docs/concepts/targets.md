---
title: Targets
weight: 10
---

# Targets

A `Target` describes what you want to image: an object, its position
on the sky, and the frames you want of it. It stays until you delete
it, across as many nights as the frames take, so you create it once and
reserve the telescope whenever the sky allows. This page explains the
fields. [Imaging](/docs/concepts/imaging/) explains what the operator
does with them during a night, and [`Target`](/docs/reference/targets/)
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

When you give a telescope to this operator, with a `Reservation` whose
`spec.holder` is `astrophotography-operator`, the operator images the
eligible targets in the reservation's namespace. It takes the target
with the highest `priority` first, and among equal priorities the
oldest. A target is eligible when:

* `suspend` is not `true`, and the `Complete` condition is not `True`
* a sequence has frames left to take
* no other `Ready` reservation is imaging it
* the reservation's telescope has an imaging camera on the target's
  `opticalTrain`. A target with no `opticalTrain` is eligible only on a
  telescope with exactly one imaging camera.

Each new reservation finds the eligible targets on its own, so you do
not edit a target for a new night.

Create the target, its archive claim, and the reservation in one
namespace. The operator's pods mount the archive claim and the
reservation's outbox claim, and a pod can mount only the claims of its
own namespace.

## Coordinates

`coordinates` are J2000, the form that SIMBAD and KStars print: the
right ascension in hours as `HH:MM:SS.s`, and the declination in
degrees as `±DD:MM:SS.s`. The API server refuses any other form, so a
right ascension in decimal hours is never read as degrees. An INDI
mount takes coordinates at the equinox of the date, so the operator
converts them from J2000 before each slew.

## Sequences, and how frames count

Each sequence is a filter, an exposure, a count, and optionally the
binning, the gain, and the offset. When you leave a field out, the
operator does not set it and the camera keeps its current setting, so
a camera with no gain property still works. The operator finishes the
first sequence that has frames left before it starts the next, because
a change of filter can need a refocus.

A frame counts toward the first sequence whose settings it matches:
the same filter, exposure, binning, gain, and offset, where a field
that the sequence leaves out matches any value. Because frames are
matched by settings and not by position, you can reorder or extend the
sequences and the frames already taken still count.

`status.sequences` shows two counts for each sequence: `archived`, the
frames in the archive's catalog, and `pending`, the frames still in an
outbox. The operator updates it, with `status.progress` and the
`Complete` condition, as soon as a new target is created and after each
edit of the sequences. A sequence has frames left while
`count - archived - pending` is above zero. A sequence can end with
more archived frames than its `count`, and the extra frames do not
count toward another sequence.

## The archive and the file names

The camera writes each frame once, under its final name, into the
reservation's outbox on the telescope's machine. The operator then
moves it to `spec.archive`: a claim of yours and a directory in it.
`archive.claimName` cannot change after you create the target,
because the counts come from the catalog in that claim. To use
another claim, create another target.

`archive.format` gives each frame's file name, with tokens such as
`{object}`, `{filter}`, `{night}`, and `{datetime}`, and
`archive.promotion` sets when frames move.
[The archive](/docs/concepts/archive/) explains both, and how the
operator moves a frame without losing it.

## The frame catalog

The catalog is one file for each target, at
`<archive.path>/catalog/<Target>.jsonl` in the archive, with one line
of JSON for each frame. It is stored with the frames, so a backup of
the archive includes it. [The archive](/docs/concepts/archive/#the-catalog)
lists the fields of a line.

## FITS keywords

Every frame's header has the keywords that `observatory-operator`
sets at each of its levels, then the target's `fitsKeywords`, then two
that this operator writes: `OBJECT`, from `spec.object`, and
`FRAMEID`, the frame's ID, a UUIDv7. The API server refuses `OBJECT`
and `FRAMEID` in `fitsKeywords`, and every keyword that the camera's
driver writes from its own readings, such as `EXPTIME`.
`observatory-operator`'s page on
[FITS keywords](https://liken.sh/observatory/docs/concepts/fits-keywords/)
explains the levels.

## The conditions

| Condition | `True` | `False` reasons |
|---|---|---|
| `Complete` | every sequence has at least its count archived | `WorkLeft` |
| `Imaging` | a reservation images the target; the reason is `Slewing`, `Centering`, `Guiding`, or `Exposing` | `Waiting`, `Suspended`, `SequencesDone`, `NoTrain`, `NoFilter`, `SlewFailed`, `CenteringFailed`, `GuidingFailed`, `OutboxFull`, `ExposureFailed` |
| `ArchiveAvailable` | the last promotion succeeded | `ClaimMissing`, `Unschedulable`, `CopyFailed`, `VerifyFailed`, `NameConflict` |
