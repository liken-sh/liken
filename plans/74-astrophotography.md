# 74, Astrophotography on a `liken` cluster

Open. Mode 1 is built: `observatory-operator` plans 01 to 17 built it,
and drilled it with the INDI simulators on the test clusters from
2026-10-05 to 2026-10-12. Mode 2, `astrophotography-operator`, is not
built. This plan holds the idea, the two components, and the contract
between them. Each component's own plans hold its design and the tests
behind it.

## The idea

A telescope rig is a set of USB and serial devices on one machine next
to the mount: the mount, a main camera, a filter wheel, a focuser, and a
guide camera. Today a small computer runs the rig, usually a Raspberry
Pi with a distribution such as StellarMate or AstroArch, and a person
drives it from a desktop application.

On a `liken` cluster, a small x86-64 machine at the mount joins the
cluster as one more node, because `liken` does not run on ARM. A rig
can also span two machines at the pier, for example one for the
cameras and one for the mount. Machines elsewhere can run a weather
station or a roll-off roof. Each device runs in its own pod with its
own DRA claim. The rest of the cluster stacks the frames, and a screen
in the house shows the session.

The rig in this plan does not travel. A rig at a remote dark site has
no control plane, and a one-node cluster adds little over the
distributions above.

## Two modes and two components

The design has two modes, and the second mode is built on the first.

1. **The rig as a service.** The cluster runs the devices and serves
   them on one INDI endpoint for each telescope. A person connects
   KStars to that endpoint and drives the night with Ekos, by schedule
   or by hand.
2. **The orchestrated night.** An operator plans the night from
   declared targets and drives the same endpoint as its client. This is
   the end goal.

The two modes split into two components, with dependencies that go in
one direction, the same way library-operator depends on media-operator.

- **`observatory-operator`** controls the hardware, with the API group
  `observatory.liken.sh`. It owns the device pods, the INDI servers,
  the guider, and the reconciler that configures each device. It has no
  information about targets or nights. Mode 1 is this component alone.
  It is named for the observatory, not the telescope, because it also
  controls the devices that several telescopes share: a dome, a weather
  station, and a roof. [Its
  design](../observatory-operator/plans/00-design.md) holds the
  topology, the resources, and the images.
- **`astrophotography-operator`** drives imaging. It owns `Target`, the
  planner, grading, and the frame rows in a `Catalog`. It depends on
  `observatory-operator`, and `observatory-operator` does not depend
  on it. [Its design](../astrophotography-operator/plans/00-design.md)
  holds frames, plate solving, and the planner.

## The contract between them

library-operator starts playback through a Kubernetes resource, a
`Play`. `astrophotography-operator` cannot drive the rig that way,
because an exposure, a dither, and a guide pulse happen too often to be
objects in the API. So the contract is a resource that names a network
endpoint:

- A `Reservation` gives one holder the use of one `Telescope`: a
  person's KStars in mode 1, or a `Session` of
  `astrophotography-operator` in mode 2. So a person can take the
  telescope from the operator and give it back.
- The `Telescope`'s status names the `Service` of its INDI server, and
  each device's status names its INDI device.
- The telescope's `Guider` runs PHD2, and both KStars and
  `astrophotography-operator` drive it over PHD2's event API on port
  4400.

## What stays upstream

The components speak to existing programs and replace none of them.

- **INDI** is the device layer. The core drivers and the simulators
  ship with the library, and vendors maintain their drivers in
  `indi-3rdparty`. Some camera drivers link closed vendor SDKs, for
  example ZWO's `libASICamera2`.
- **PHD2** guides the mount. It is the only guider on Linux that runs
  as a standalone program and has an API. The other guiders are built
  into larger programs: Ekos and CCDciel have their own, and INDIGO's
  guider agent belongs to INDIGO, a separate device framework.
- **astrometry.net** solves frames for `astrophotography-operator`. In
  mode 1, KStars solves on the client side with StellarSolver and its
  own index files, so the cluster needs no solver for that mode.

`astrophotography-operator` does not drive the rig through Ekos. The
Ekos Scheduler is scriptable over DBus
([docs](https://kstars-docs.kde.org/en/user_manual/ekos-scheduler.html)),
and it would bring years of focus, solve, and flip logic. It would also
put KStars, a Qt desktop application, inside a pod, and make its DBus
interface the operator's real API.

## Security

INDI has no authentication. Any client that reaches port 7624 can slew
the mount, and any client that reaches port 4400 can command PHD2. The
cluster owner chooses both the path from outside the cluster and the
`NetworkPolicy`, because a policy that the operator wrote would block
the path the owner chose. `observatory-operator` [plan
10](../observatory-operator/plans/completed/10-access-from-a-desktop.md)
records the decision, and the operator's manual names the labels and
ports that the owner's objects select.

## Adjacent work this plan does not cover

The screen in the house shows that some pieces of the media stack
serve more than media. Each needs its own plan, and this plan does not
depend on them.

- **The bus.** media-operator installs the Mosquitto broker at
  `bus.liken-system.svc:1883`, and only the media domain uses it today.
  The rig does not use the bus, because INDI carries every device
  message. Only the screen in the house would make an astronomy
  component a client of the bus. The broker could become its own
  component.
- **Input.** `Remote`, `Keymap`, and the reader pod in media-operator
  turn a controller's events into presses, and none of that is specific
  to media. They could move to an input operator.
- **Switching programs on a screen.** A person needs a way to move a
  screen from one program to another with a press, for example from the
  media browser to the astronomy screen. Today a person edits
  `spec.idle.controller`.

## Open questions

- Whether the screen in the house is part of
  `astrophotography-operator` or its own component.
- [Where frames are
  stored](../astrophotography-operator/plans/open-problems/where-frames-are-stored.md):
  the camera's pod belongs to `observatory-operator`, and the frames,
  their grading, and their solving belong to `astrophotography-operator`.
