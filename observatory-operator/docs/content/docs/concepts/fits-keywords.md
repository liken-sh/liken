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
`Telescope`, a `Camera`, or the `Reservation`. The operator gives them
to the driver before the first exposure, so the driver writes every
keyword when it creates the file, and no program edits the frame
afterward.

## Declared and measured keywords

A measured keyword can change from one frame to the next, such as
`DATE-OBS`, `EXPTIME`, `CCD-TEMP`, the mount's coordinates, and the
filter. A declared keyword is the same on every frame, such as
`OBSERVER`, the name of the telescope, or a keyword of your own.

The driver does not protect what it measures. A declared `EXPTIME` of
`999` replaces the real length of the exposure on every later frame.
A header that is wrong is worse than a header that lacks a keyword, so
the API server refuses a resource that declares a keyword that the
driver writes, and its message names the keyword. These names are
refused:

- the structure of the file: `SIMPLE`, `BITPIX`, `NAXIS`, `NAXIS1`,
  `NAXIS2`, `EXTEND`, `BZERO`, `BSCALE`, and `ROWORDER`
- the measures of the exposure: `EXPTIME`, `CCD-TEMP`, `PIXSIZE1`,
  `PIXSIZE2`, `XBINNING`, `YBINNING`, `XPIXSZ`, `YPIXSZ`, `FRAME`,
  `IMAGETYP`, `FILTER`, `FOCALLEN`, `APTDIA`, `ROTATANG`, `FOCUSPOS`,
  `FOCUSTEM`, `SCALE`, `SITELAT`, `SITELONG`, `AIRMASS`, `OBJCTAZ`,
  `OBJCTALT`, `OBJCTRA`, `OBJCTDEC`, `RA`, `DEC`, `PIERSIDE`,
  `EQUINOX`, `DATE-OBS`, `GAIN`, `OFFSET`, `DARKTIME`, and `MPSAS`
- the keywords of a color sensor and of the range of the pixels:
  `BAYERPAT`, `XBAYROFF`, `YBAYROFF`, `DATAMIN`, and `DATAMAX`
- the world coordinate system: `CTYPE1`, `CTYPE2`, `CRVAL1`,
  `CRVAL2`, `CRPIX1`, `CRPIX2`, `CDELT1`, `CDELT2`, `CROTA1`,
  `CROTA2`, `SECPIX1`, `SECPIX2`, and `RADECSYS`
- `COMMENT`, `HISTORY`, and `END`, which FITS reserves for cards that
  hold no value

The driver writes `INSTRUME` and `TELESCOP` too, but it fills them with
the names of INDI devices, such as `CCD Simulator` and `Telescope
Simulator`. Those name a driver, not your equipment, so you can
declare both, and your value replaces the driver's. The driver writes
`IMAGETYP` from the frame type of the exposure, in the form of
SBFITSEXT, the FITS extension that SBIG and Diffraction Limited
published for amateur cameras, such as `Light Frame`.

A keyword name is 1 to 8 of the capital letters `A` to `Z`, the
digits, `-`, and `_`. A value holds at most 68 characters, the longest
text that one 80-column card holds, and a comment at most 47. Both
hold only printable ASCII characters, because FITS allows no others in
a header. The driver writes a value that reads as a number, such as
`2026` or `-4.5`, as a number, and any other value as quoted text.
A value cannot hold `/`: libindi's CCD writes each `/` in a value as
`\`, so `f/5` would reach the header as `f\5`, and the API server
refuses it. A comment can hold `/`.

## The levels

Each level declares the keywords that are true of everything below
it. The operator merges the four lists in the order `Observatory`,
`Telescope`, `Camera`, `Reservation`, and a keyword at a later level
replaces the value and the comment of the same name at an earlier
one. Each name keeps the place where it first appears. With these
four resources:

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

the frames of `east-main` during `east-tonight` carry `OBSERVER` as
`Guest`, then `SITENAME`, `TELESCOP`, `INSTRUME`, and `OBJECT`. The
other fields of each spec are left out here. To read the merged list of
each camera:

```sh
kubectl get reservation east-tonight -n observatory -o jsonpath='{.status.session.fitsKeywords}'
```

## How long keywords last

The driver keeps each keyword that it receives and writes it on every
later frame, until its process exits or a client clears every keyword
with a write of the name `INDI_CLEAR`. So the operator writes each
keyword once for the session:

- The `PowerOn` step records the merged list of each imaging camera in
  the reservation's `status.session`. A camera that a `Guider` guides
  with sends its frames to PHD2 and writes no file, so it gets no
  keywords.
- The `Configure` step clears each imaging camera's keywords, and
  then writes the record to its `FITS_HEADER`, one write for each
  keyword. Every frame of the
  session carries them, and so does each frame that KStars and Ekos
  take. A camera whose driver lacks `FITS_HEADER` gets a note in the
  step's summary.
- A driver that comes back after its pod or the server restarts is a
  new process with no keywords. The operator configures the camera
  again, and writes the record again.

An edit of `spec.fitsKeywords` at any level takes effect at the next
session, so every frame of one night carries the same declared
keywords. A retry of a failed activation step records the keywords
again from the specs, and when they changed, it runs `Configure` again,
which clears the camera's keywords and writes the new list, so a
keyword that the retry leaves out is gone from the next frame. A
program that writes keywords of its own, such as `OBJECT`, writes them
again after a retry.
