# 05, The property schema

Proposed on 2026-10-05. Not built.

Plan 06 wrote by hand the few typed fields that activation needs, such
as a camera's `temperature` setpoint and a filter wheel's `filters`, in
the package `observatory` and the CRDs in `deploy/`. This plan
generates the fields of the other standard properties, and the
generated fields join those types and CRDs.

## The problem

The resources need typed fields for the properties that every driver
of a kind shares, and INDI has no machine-readable schema for them.
The standard properties are documented as tables: 132 properties, each
with a name, a type, its members, and a one-line description. The
tables give no permissions, no switch rules, and no ranges, and units
appear only in the prose, such as "JNow RA, hours". A driver states
types, permissions, rules, and ranges only when it runs, in its
`def*Vector` messages, and never states units.

## The requirement

A generator builds one schema file for each device kind from two
sources:

- The structure, from the simulators. Each simulator inherits the INDI
  base class of its kind, so its definitions are the standard
  properties as the library implements them.
- The descriptions, from `drivers/standard-properties.md` in
  `indilib/docs`.

We curate the result: we add the units that the prose states, choose
which properties become typed fields, and name the fields. Code
generation then writes the Go types and the CRD schemas from the
curated file.

When INDI is bumped, the generator runs again, and its diff against
the curated file shows each change upstream. The generator reports
every place where the documentation and the implementation disagree,
and picks neither.

## How we test it

The generator's report on the current INDI release, read by a person.

## Upstream issues

- [docs#7](https://github.com/indilib/docs/issues/7) and
  [docs#8](https://github.com/indilib/docs/pull/8): the
  standard-properties page is maintained by hand. No upstream effort to
  publish a machine-readable schema was found.
- [indi#1311](https://github.com/indilib/indi/issues/1311): mount
  drivers lacked `EQUATORIAL_COORD`, although it is a standard
  property. The generator reports such gaps.

## References

- Standard properties: <https://docs.indilib.org/drivers/standard-properties/>,
  from <https://github.com/indilib/docs>
- The base classes: `libs/indibase/inditelescope.cpp`,
  `libs/indibase/indiccd.cpp`, and the focuser and filter interfaces in
  `libs/indibase/` in `indilib/indi`
- `libs/indidevice/indistandardproperty.h` names the general and
  connection properties
- The interface bits in `DRIVER_INFO.DRIVER_INTERFACE`
