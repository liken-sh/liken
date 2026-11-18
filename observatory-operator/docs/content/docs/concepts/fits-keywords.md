---
title: FITS keywords
weight: 35
---

# FITS keywords

A camera saves each frame as a FITS file, and the file's header tells
every later tool what the frame is: when the exposure started, how
long it was, which filter was in the light path, and who took it. The
camera's INDI driver writes most of the header from what it measures
when it takes the frame. You add the keywords that it cannot measure,
such as `OBSERVER`, in `spec.fitsKeywords` of the `Observatory`, the
`Telescope`, a `Camera`, or the `Reservation`. The operator sends them
to the driver before the first exposure, so the driver writes them into
each file when it creates the file, and no program edits the frame
afterward.

## Declared and measured keywords

A measured keyword can change from one frame to the next, such as
`DATE-OBS`, `EXPTIME`, `CCD-TEMP`, the mount's coordinates, and the
filter. A declared keyword is the same on every frame, such as
`OBSERVER`, the name of the telescope, or a keyword of your own.

The driver does not check a declared keyword against its own
measurements. A declared `EXPTIME` of `999` replaces the real length of
the exposure on every later frame. A wrong header is worse than a
header that lacks a keyword, so the API server refuses a resource that
declares a keyword that the driver writes, and its message names the
keyword. It refuses these names:

- the structure of the file: `SIMPLE`, `BITPIX`, `NAXIS`, `NAXIS1`,
  `NAXIS2`, `EXTEND`, `BZERO`, `BSCALE`, and `ROWORDER`
- the values that the driver measures when it takes the frame: `EXPTIME`, `CCD-TEMP`, `PIXSIZE1`,
  `PIXSIZE2`, `XBINNING`, `YBINNING`, `XPIXSZ`, `YPIXSZ`, `FRAME`,
  `IMAGETYP`, `FILTER`, `FOCALLEN`, `APTDIA`, `ROTATANG`, `FOCUSPOS`,
  `FOCUSTEM`, `SCALE`, `SITELAT`, `SITELONG`, `AIRMASS`, `OBJCTAZ`,
  `OBJCTALT`, `OBJCTRA`, `OBJCTDEC`, `RA`, `DEC`, `PIERSIDE`,
  `EQUINOX`, `DATE-OBS`, `GAIN`, `OFFSET`, `DARKTIME`, and `MPSAS`
- the keywords of a color sensor and of the range of pixel values:
  `BAYERPAT`, `XBAYROFF`, `YBAYROFF`, `DATAMIN`, and `DATAMAX`
- the world coordinate system: `CTYPE1`, `CTYPE2`, `CRVAL1`,
  `CRVAL2`, `CRPIX1`, `CRPIX2`, `CDELT1`, `CDELT2`, `CROTA1`,
  `CROTA2`, `SECPIX1`, `SECPIX2`, and `RADECSYS`
- `COMMENT`, `HISTORY`, and `END`, which FITS reserves for cards that
  hold no value

The API server also refuses `FRAMEID`, with a message that names
`astrophotography-operator`, because that operator writes a different
`FRAMEID` into each frame that it takes. A declared `FRAMEID` would give
every frame of the session the same identity, including the frames that
KStars and Ekos take.

The driver also writes `INSTRUME` and `TELESCOP`, but it fills them
with the names of INDI devices, such as `CCD Simulator` and `Telescope
Simulator`. Those names identify a driver, not your equipment, so you
can declare both, and your value replaces the driver's.

A keyword name is 1 to 8 characters from the capital letters `A` to
`Z`, the digits, `-`, and `_`. A value is 1 to 68 characters, the
most that one 80-column FITS card has room for, and a comment is at
most 47. Both must be printable ASCII, because FITS allows no other
characters in a header. The driver writes a value that parses as a
number, such as `2026` or `-4.5`, as a number, and any other value as
quoted text. A value cannot contain `/`, because libindi's CCD class writes
each `/` in a value as `\`, so `f/5` would reach the header as `f\5`.
A comment can contain `/`.

## The levels

Each level declares the keywords that are true of everything below
it. The operator merges the four lists in the order `Observatory`,
`Telescope`, `Camera`, `Reservation`. A keyword at a later level
replaces the value and the comment of the same name at an earlier
level, and the merged list keeps each name at the position where it
first appears. With these four resources:

```yaml
kind: Observatory
metadata:
  name: lab
spec:
  fitsKeywords:
  - {name: OBSERVER, value: Simulated Observer}
  - {name: SITENAME, value: Back yard}
---
kind: Telescope
metadata:
  name: east
spec:
  fitsKeywords:
  - {name: TELESCOP, value: East refractor, comment: the Telescope's own name}
---
kind: Camera
metadata:
  name: east-main
spec:
  fitsKeywords:
  - {name: INSTRUME, value: Main camera}
---
kind: Reservation
metadata:
  name: east-tonight
spec:
  fitsKeywords:
  - {name: OBSERVER, value: Guest}
  - {name: OBJECT, value: M42}
```

the frames of `east-main` during `east-tonight` carry the keywords
`OBSERVER` (with the value `Guest`), `SITENAME`, `TELESCOP`, `INSTRUME`,
and `OBJECT`, in that order. The example leaves out the other fields of
each spec. To read the merged list of each camera:

```sh
kubectl get reservation east-tonight -n observatory -o jsonpath='{.status.session.fitsKeywords}'
```

## How long keywords last

The driver keeps each keyword that it receives and writes it on every
later frame, until its process exits or a client clears every keyword
by writing the name `INDI_CLEAR`. So the operator writes each keyword
once for the session:

- The `PowerOn` step records the merged list of each imaging camera in
  the reservation's `status.session`. A camera that a `Guider` uses
  for guiding sends its frames to PHD2 and writes no file, so it gets
  no keywords.
- The `Configure` step clears each imaging camera's keywords, and then
  writes the recorded list to the camera's `FITS_HEADER` property, one
  write for each keyword. Every frame of the session carries them,
  including the frames that KStars and Ekos take. When a camera has
  keywords and its driver has no `FITS_HEADER` property, the step's
  summary says so.
- When a driver restarts with its pod or with the server, it starts
  with no keywords. The operator configures the camera again, which
  writes the recorded list again.

An edit of `spec.fitsKeywords` at any level takes effect at the next
session, so every frame of one night carries the same declared
keywords. The exception is a retry of a failed activation step, which
records the keywords again from the specs. When the list changed, the
retry runs `Configure` again, which clears the camera's keywords and
writes the new list. A program that writes keywords of its own, such as
`OBJECT`, must write them again after such a retry.
