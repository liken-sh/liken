# 18, Saved horizons

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the resource and its fields
are not designed yet.

## The problem

A telescope sees less sky than its site's latitude allows, because
trees, buildings, and roofs block part of it. When
`astrophotography-operator` chooses what to image, it has to know which
part, or it picks a target behind the neighbor's house. The blocked
part depends on where the telescope stands: two piers at one site see
different treelines, and a portable telescope moved from the yard to a
roof sees a different sky.

## Decisions

- **A horizon belongs to a telescope's position, not to the site.** The
  `Observatory` has one set of coordinates, but each `Telescope` has
  its own view of the sky from where it stands.
- **A person saves several named horizons**, such as `yard` and `roof`,
  and associates one with the `Telescope` for where it stands now.
  Moving a portable telescope means choosing another saved horizon,
  not tracing the sky again.
- **Horizons use the format the hobby already shares**: azimuth and
  altitude pairs in degrees, one pair to a line, which N.I.N.A.,
  SkyTools, and Stellarium read. A person brings a horizon they traced
  with a phone app or made for another program, and can take ours to
  those programs.
- **The feature belongs to `observatory-operator`.** A horizon is a
  fact about where the hardware stands, which is what this operator's
  resources describe. `astrophotography-operator` reads it, the same
  way it reads the site's coordinates.
- **A horizon is not a mount limit, and not a dome's slit.** A horizon
  says where the sky is blocked, and only the scheduler uses it. A
  mount limit says where the hardware must not move, which is a safety
  matter. A dome's slit moves with the dome, so it is no fixed horizon.

## Still open

The resource or field that holds a saved horizon, how a `Telescope`
names its current one, and the exact syntax the other programs accept,
such as comments and the order of the columns.
