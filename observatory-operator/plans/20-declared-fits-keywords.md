# 20, Declared FITS keywords

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the fields are not designed
yet.

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

## Still open

The field on each resource, the list of keywords that the driver owns,
and how the two operators share the camera's property. Not checked:
whether a value set through `FITS_HEADER` stays for later exposures or
must be set before each one. The answer decides how the two operators
interleave their writes.

Keywords written into archived frames are the hardest decision to
change later, because the frames never change. The keyword list
deserves the most care of anything in this plan.
