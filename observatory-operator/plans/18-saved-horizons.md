# 18, Saved horizons

Stub. Proposed on 2026-10-12. Not designed.

A telescope sees less sky than its site's latitude allows, because
trees, buildings, and roofs block part of it. A scheduler that plans
a night has to know which part, or it picks targets behind the
neighbor's house. The obstruction depends on where the telescope is
set up, not on the site: two piers at one site see different
treelines, and a portable telescope moved from the yard to a roof
sees a different sky.

So a person saves several named horizons, such as `yard` and `roof`,
and associates one with the `Telescope` for where it stands now.
`astrophotography-operator` reads the telescope's current horizon when
it decides what is visible.

A horizon uses the format the hobby already shares: azimuth and
altitude pairs in degrees, one pair to a line, which NINA, SkyTools,
and Stellarium read. A person can bring a horizon they traced with a
phone app or made for another program.

A horizon is not a mount limit. It says where the sky is blocked, and
the scheduler uses it. A mount limit says where the hardware must not
go, and it is a safety matter. A dome's slit moves with the dome, so it
is not a fixed horizon either.

To design: the resource or field that holds a saved horizon, how a
`Telescope` names the current one, and the exact file syntax that the
other programs accept.
