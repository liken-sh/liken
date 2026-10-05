# 06, The resources

Proposed on 2026-10-05. Not built.

## The problem

`astrophotography-operator`, KStars users, and the screens all depend
on `observatory-operator` through its resources. media-operator and
library-operator meet at `Play`, and equipment-operator models each
`Receiver` and `Television` with fields that every model shares and
fields that one vendor adds. The observatory needs an interface of
the same quality between its layers, or the layers above it will read
INDI properties by name and break when a driver changes.

## The requirement

The CRDs in `observatory.liken.sh`:

- An observatory resource for what its telescopes share: the INDI
  server, the weather, the safety state, and later a roof.
- A resource for each device, by kind: mount, camera, filter wheel,
  focuser, rotator, dome, weather station, and the others that INDI's
  interface bits name. Each has typed fields for the standard
  properties of its kind, generated in plan 05.
- Vendor properties appear in status as the device defines them: the
  name, the type, the permission, the limits, the switch rule, and the
  label. A person can set one by name. No typed field depends on one.
- The resource that names the INDI endpoint, the contract that
  `astrophotography-operator` and KStars connect through.

How the device kinds and the `Telescope` relate is open. A telescope
groups a mount, cameras, and a focuser, and a weather station belongs
to no telescope.

## How we test it

The CRDs apply on the `dev-cluster`, and validation refuses a resource
that breaks the schema. No controller runs yet.

## References

- INDI's interface bits: `DRIVER_INTERFACE` in `DRIVER_INFO`, and the
  interface pages under <https://docs.indilib.org/interfaces/>
- equipment-operator's `Receiver` and `Television` resources
- [Root plan 74](../../plans/74-astrophotography.md), "Resources"
