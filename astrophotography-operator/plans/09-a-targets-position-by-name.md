# 09, A target's position by name

Proposed on 2026-10-15, in the design conversation of [plan
04](04-the-scheduler.md). Not built.

## The problem

A `Target` needs `coordinates` today, in the form that SIMBAD and
KStars print, so a person looks each object up before they declare it.
Most people know an object by its name: M42, NGC 7000, the Orion
Nebula. And a target whose position moves, such as a planet, the moon,
or a comet, has no fixed coordinates to write.

## Decisions

- **One `Target`, with a choice of position.** Where an object is and
  how it is captured are separate questions. A comet moves but takes
  long guided exposures, so a split into a deep-sky kind and a
  solar-system kind would leave it in neither. The position becomes a
  one-of, the way a pod's volume names exactly one source:
  `coordinates`, as today, or a name. The capture method belongs to
  the sequence, beside the filter and the exposure. The API is
  `v1alpha1`, and `coordinates` becomes the first choice of the
  one-of, so no target breaks.
- **A name resolves against a catalog in the image first.**
  [OpenNGC](https://github.com/mattiaverga/OpenNGC) lists every NGC
  and IC object with its Messier number and common names, and its
  addendum holds the few objects outside NGC and IC, such as M45. Its
  `NGC.csv` is about 4 MB, and the repository was last pushed on
  2026-10-10. It also gives each object's size, for framing later. The
  lab and a dark site can have no network, so the lookup works with
  none.
- **An online resolver is the fallback**, such as SIMBAD's Sesame,
  for a name that the catalog does not hold.
- **The resolved position is written into the target's status**, so a
  person sees what the name resolved to, and every frame records the
  same position. `coordinates`, when given, win over the name.
- **OpenNGC is CC-BY-SA-4.0.** The image carries its attribution and
  its license, the way `notices/` carries the license of each
  third-party component.
- **Solar-system bodies use their IAU names**: `moon`, `jupiter`, and
  the Minor Planet Center's designations for comets and asteroids,
  such as `C/2020 F3` and `1P/Halley`. The integer IDs of JPL's NAIF,
  such as `599` for Jupiter, are precise and unreadable in YAML.
  These bodies belong to [plan 10](10-planetary-and-solar-imaging.md).

## Still open

The field names, which catalog fields the status keeps, and how often
the image takes a new release of OpenNGC.
