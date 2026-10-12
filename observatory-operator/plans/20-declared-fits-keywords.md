# 20, Declared FITS keywords

Stub. Proposed on 2026-10-12. Not designed.

A frame's FITS header has two kinds of keywords. The driver measures
some of them at capture: `DATE-OBS`, `EXPTIME`, `CCD-TEMP`, the
mount's coordinates, and the filter. A person declares the others,
and they do not change from frame to frame: `OBSERVER`, `TELESCOP`,
the site, and anything the person wants on every frame. The goal is
a header that is complete under the FITS standard, SBFITSEXT, and
the practice of the established imaging programs, with nothing in it
that is false.

Each resource can declare keywords: the `Observatory`, the
`Telescope`, the `Camera`, and the `Reservation`. The operator overlays
them, the most specific level winning, and sets them on the camera
through its `FITS_HEADER` property, so the driver writes them into each
frame when it creates the file. `astrophotography-operator` adds the
levels it owns, such as the target, the same way. A declared value
never replaces a measured keyword; a resource that tries is refused.
A driver that measures a value wrongly is a bug to report upstream,
not to correct here.

To design: the field on each resource, the list of keywords a
measured value owns, and how the two operators share the property.
Not checked: whether a value set through `FITS_HEADER` stays for later
exposures or must be set before each one. The answer decides how the
two operators interleave.
