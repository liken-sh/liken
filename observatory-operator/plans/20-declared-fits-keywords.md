# 20, Declared FITS keywords

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
answers what the design conversation left open, from the evidence of
the CCD simulator.

## The problem

A frame's FITS header is how every later tool knows what the frame is.
`liken`'s astronomy components aim to write the most complete and
correct headers that any imaging program writes. That means the
keywords of the FITS standard, of SBFITSEXT, the extension that SBIG
and Diffraction Limited published for amateur cameras, and of common
practice, which N.I.N.A. documents keyword by keyword. A person also
wants their own keywords on every frame.

## Decisions

- **A header has two kinds of keywords, and the operator treats them
  differently.** The driver measures some at capture: `DATE-OBS`,
  `EXPTIME`, `CCD-TEMP`, the mount's coordinates, the filter. A person
  declares the others, and they do not change from frame to frame:
  `OBSERVER`, `TELESCOP`, the site, and anything custom.
- **Declared keywords overlay down a hierarchy.** The `Observatory`,
  the `Telescope`, the `Camera`, and the `Reservation` can each declare
  keywords, and the most specific level wins.
  `astrophotography-operator` continues the hierarchy with the levels
  it owns, such as the target.
- **A declared value never replaces a measured one.** A header that
  lies is worse than a header that lacks a keyword, so a resource that
  declares a measured keyword is refused when it is applied, not
  quietly overridden.
- **A driver that measures wrongly is a bug to report upstream.** The
  operator does not patch over drivers.
- **The driver writes every keyword when it creates the file.** The
  operator sets the declared keywords on the camera through its
  `FITS_HEADER` property before an exposure. No component edits a
  frame after the driver writes it, so a frame is complete and final
  from the moment it exists. Frames in mode 1 carry the keywords too.
- **Where the conventions disagree, the person chooses.** For example,
  `IMAGETYP` is `Light Frame` in SBFITSEXT and `LIGHT` in the IRAF
  style.

## What the driver does

On 2026-10-12, the CCD simulator of the pinned `indi-simulators` image
(`20261005-5`) ran in Docker on the laptop with the mount, focuser,
rotator, filter wheel, and SQM simulators, and a client wrote
`FITS_HEADER` between exposures and read the headers of the files that
the driver saved:

- **A keyword stays.** One write puts the keyword on every later frame,
  until the driver process exits. A disconnect and a connect do not
  clear it.
- **A second write of the same name replaces the value**, and a new
  name adds a keyword.
- **A keyword cannot be removed.** A write with an empty value changes
  nothing.
- **A written keyword replaces the driver's own.** A write of `EXPTIME`
  as 999 put `EXPTIME = 999` on the next frame, in place of the
  exposure's real length. The driver does not protect what it
  measures, so the operator must.
- **The driver infers the type.** A value that reads as a number is
  written as a number, and any other value is written as a quoted
  string.
- **The driver writes these keywords**, with every simulator connected:
  the structure of the file (`SIMPLE`, `BITPIX`, `NAXIS`, `NAXIS1`,
  `NAXIS2`, `EXTEND`, `BZERO`, `BSCALE`, `ROWORDER`), the names of the
  INDI devices (`INSTRUME`, `TELESCOP`), and the measures of the
  exposure: `EXPTIME`, `CCD-TEMP`, `PIXSIZE1`, `PIXSIZE2`, `XBINNING`,
  `YBINNING`, `XPIXSZ`, `YPIXSZ`, `FRAME`, `IMAGETYP`, `FILTER`,
  `FOCALLEN`, `APTDIA`, `ROTATANG`, `FOCUSPOS`, `FOCUSTEM`, `SCALE`,
  `SITELAT`, `SITELONG`, `AIRMASS`, `OBJCTAZ`, `OBJCTALT`, `OBJCTRA`,
  `OBJCTDEC`, `RA`, `DEC`, `PIERSIDE`, `EQUINOX`, `DATE-OBS`, and
  `GAIN`. The header is in `indi/testdata/ccd/frame-header.txt`.
  libindi's CCD also writes `OFFSET`, `DARKTIME`, and `MPSAS` when the
  camera or an SQM reports them; the simulators did not.

## The design

- **The field is `spec.fitsKeywords`** on the `Observatory`, the
  `Telescope`, the `Camera`, and the `Reservation`: a list of `name`,
  `value`, and an optional `comment`, keyed by `name`. The CRD marks
  it a list of type `map`, so server-side apply merges one keyword at a
  time, and the header keeps the order the person wrote.
- **The schema refuses a keyword the driver owns.** The CRD's rules
  refuse a name that is not a FITS keyword (one to eight of `A`-`Z`,
  `0`-`9`, `-`, and `_`), and a name on the driver's list above,
  except `INSTRUME` and `TELESCOP`. The API server refuses such a
  resource when it is applied, so a mistake never waits for a night to
  show. `INSTRUME` and `TELESCOP` are declarable, because the driver
  fills them with the names of INDI devices, such as `CCD Simulator`,
  which name a driver and not the person's equipment. `COMMENT`,
  `HISTORY`, and `END` are refused too, because they are not
  keywords with values.
- **The most specific level wins.** The operator merges the four lists
  in the order `Observatory`, `Telescope`, `Camera`, `Reservation`, and
  a name at a later level replaces the value of the same name at an
  earlier one.
- **`Configure` writes the merged list once**, one write of
  `FITS_HEADER` for each keyword, to each imaging camera of the
  session. A guide camera writes no files, so it gets none. Because a
  keyword stays until the driver exits, and a session starts each
  camera's driver once, one write covers every frame of the session,
  including the frames of a desktop client in mode 1.
- **An edit takes effect at the next session.** The session records the
  merged list when `PowerOn` records the devices, as [plan
  17](completed/17-a-session-freezes-its-devices.md) does for every
  other spec. A keyword cannot be removed from a running driver, so a
  live edit could add and replace but never remove, and the frames of
  one night would disagree with each other.
- **A choice of convention belongs to whoever sets the frame type.**
  The driver writes `IMAGETYP` in the SBFITSEXT form, `Light Frame`,
  from the frame type of the exposure. `IMAGETYP` is on the driver's
  list, so a person cannot declare it. A program that sets the frame
  type, such as `astrophotography-operator`, can write the form the
  person chose for that frame type, because it writes the fact the
  driver measures in another spelling and not a different fact.

## Still open

`astrophotography-operator` writes keywords of its own levels, such as
`OBJECT` for each target, into the same running driver. A keyword
cannot be removed, so a keyword that one target declares stays on the
frames of the next target unless that target writes the same name. Its
[plan 01](../../astrophotography-operator/plans/01-run-a-targets-sequence.md)
has to account for this. A way to remove a keyword through
`FITS_HEADER` is a reasonable request to libindi.

Keywords written into archived frames are the hardest decision to
change later, because the frames never change. The keyword list
deserves the most care of anything in this plan.
