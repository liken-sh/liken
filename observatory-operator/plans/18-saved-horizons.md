# 18, Saved horizons

Proposed on 2026-10-12, and designed on 2026-10-12. The design below
answers what the design conversation left open.

Built on 2026-10-12. Drilled on the QEMU dev cluster on 2026-10-12;
[the drill](#the-drill) records the results.

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

## The design

- **A saved horizon is a `Horizon`**, a resource in the namespace of
  the observatory, named for the place, such as `yard` or `roof`. A
  resource of its own lets a person list the saved horizons with
  `kubectl get horizons`, and lets several telescopes name one.
- **`spec.profile` holds the file as the person brought it**: one
  point to a line, the azimuth and then the altitude, in degrees,
  separated by spaces, a tab, or a comma. A line that starts with `#`
  is a comment, and a blank line is skipped. This is the format of
  N.I.N.A.'s custom horizon and of Cartes du Ciel's, so a person
  pastes a file in and copies it out with no conversion.
- **The schema refuses a profile that no program could read**: a line
  that is not a comment and not two numbers, an azimuth outside 0 to
  360, an altitude outside -90 to 90, or fewer than two points, which
  N.I.N.A. also requires. A mistake fails when it is applied.
- **The altitude between two points is a straight line in azimuth**,
  and the profile wraps from its last point to its first through 360
  degrees. N.I.N.A. interpolates the same way. The rule lives in Go
  beside the type, in `observatory/`, so `astrophotography-operator`
  reads a horizon with the same code that its tests use.
- **`spec.horizon` on the `Telescope` names its current `Horizon`.**
  Moving a portable telescope from the yard to the roof is one edit
  of that name. With no name, the telescope has no horizon, and only
  the sky's own geometry limits it.
- **The `Telescope`'s `HorizonFound` condition** reports whether the
  named `Horizon` exists. A telescope that names a missing horizon
  still runs, because a horizon matters only to the scheduler, but the
  condition and its `Event` tell the person before a night is spent
  on targets behind the trees.

## The build

- **The schema's rules match the whole profile with one regular
  expression each.** A rule on each line of `split` estimated more than
  100 times the API server's cost budget at a `maxLength` of 8192,
  because the estimate grows with the square of the length, and the
  API server refused the CRD. A rule on the whole string costs the
  length alone, so `maxLength` is 16384, a point for every half degree
  with comments. The ranges of the azimuth and the altitude are in the
  expressions of their numbers. `ParseProfile` reads the lines in Go,
  and a test runs one table of profiles through both and requires the
  same verdict.
- **A `Horizon` has no status**, because the operator reads nothing in
  it. Its CRD has no status subresource, and the operator's
  `ClusterRole` grants only `get`, `list`, and `watch` on it.
- **The operator watches the `Horizon`s like every other kind**, so
  the status writer composes `HorizonFound` again when a `Horizon` is
  created or deleted. `False` posts a Warning, because a scheduler
  would plan the night with no horizon.

## The drill

The drill ran on 2026-10-12 on the QEMU dev cluster, on the lab of
[plan 19's drill](19-the-frames-outbox.md#the-drill): `node-1`, a
leader with 2 GB of memory, and `node-4`, a follower with 3 GB. A
`NoSchedule` taint on `node-1` kept the observatory's pods on
`node-4`. The operator was built from d8d0759a and reached the lab
through the lab registry as `ghcr.io/liken-sh/observatory-operator:dev`.
The observatory was a copy of `examples/simulators.yaml` without its
`Reservation`, so `Horizon yard` and `Horizon roof` existed, and
`Telescope east` named `yard`. The times below come from polling the
`Telescope` every 0.5 s.

| Check | Result |
|---|---|
| Create a `Horizon` with the line `north 20` | Refused: "each line of spec.profile is a comment that starts with #, a blank line, or an azimuth and an altitude in degrees, separated by spaces, a tab, or a comma". |
| Create a `Horizon` with the azimuth 400, and one with -1 | Both refused: "each azimuth in spec.profile is from 0 to 360 degrees". |
| Create a `Horizon` with the altitude 95, and one with 90.1 | Both refused: "each altitude in spec.profile is from -90 to 90 degrees". |
| Create a `Horizon` with one point | Refused: "spec.profile holds at least two points". |
| Create three `Horizon`s that a program can read | Created: a profile with a comment, a blank line, a comma, and a tab; a profile of 360 lines, one point for each degree, in 4,856 bytes of YAML; and the points `0 -90`, `359.5 90`, and `360 -2.5`. |
| Delete `Horizon yard` | `HorizonFound` was `False` 1.7 s later, with the reason `HorizonMissing` and the message "Missing Horizon yard". A Warning `Event` `HorizonMissing` had the same message. |
| Apply the example again, which creates `yard` | `HorizonFound` was `True`, "Found Horizon yard", 4.8 s after the apply of the whole example started. A Normal `Event` `Found` had the same message. |
| Set `spec.horizon` to `roof` | 1.7 s later the message was "Found Horizon roof". The status stayed `True`, so `lastTransitionTime` kept its value and no `Event` was posted. |
| Remove `spec.horizon` | The condition was gone from the status 1.1 s later, and no `Event` was posted. |
| Set `spec.horizon` to `nowhere`, and reserve `east` | `HorizonFound` was `False`, "Missing Horizon nowhere". The reservation was `Ready` 35 s after its creation, and the `Telescope`'s `Ready` was `True` beside `HorizonFound` `False`. |
| Ask the API server what the operator's service account can do with `horizons` | `get`, `list`, and `watch`: yes. `create`, `update`, `patch`, and `delete`: no. The CRD has no status subresource. |

The checks of the profile went to the API server with `kubectl create`,
so each refusal is the message of the CRD's own rule.

## Still open

Stellarium's landscapes and SkyTools have horizon files of their own,
with more columns. A person converts one of those by hand.
