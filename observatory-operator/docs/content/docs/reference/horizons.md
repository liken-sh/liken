---
title: Horizon
weight: 112
toc: true
---

<!-- Generated from deploy/horizons-crd.yaml by crdref. Do not edit. -->

# `Horizon`

A `Horizon` is the part of the sky that trees, buildings, and roofs block from one place, such as `yard` or `roof`. A `Telescope` names its current horizon in `spec.horizon`, so a portable telescope that moves to another place takes another saved `Horizon` with one edit. Neither `observatory-operator` nor `astrophotography-operator` reads the profile today, and neither one checks a target against it before a slew. So a mount slews to a target behind the trees or below the horizon.

## spec

The horizon's altitude in each direction.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--profile"></span>`profile` | string | yes | The horizon as a text file, in the format of N.I.N.A.'s custom horizon and of Cartes du Ciel's: one point to a line, the azimuth from 0 to 360 and then the altitude from -90 to 90, in degrees, separated by spaces, a tab, or a comma. A line that starts with `#` is a comment, and a blank line is skipped. A profile needs at least two points. Between two points, the altitude changes in a straight line with the azimuth, and the last point joins the first through 360 degrees. |
