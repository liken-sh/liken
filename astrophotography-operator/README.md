# astrophotography-operator

`astrophotography-operator` takes the pictures that you ask for on a
telescope of a [`liken`](https://liken.sh/) cluster. You declare a
`Target`: an object, its coordinates, and the frames that you want of
it, such as five-minute frames of M42 through each of your filters.
When you reserve a telescope for the operator, it chooses a target
and slews to it. It centers the target with a plate solve, guides and
dithers with PHD2, and takes the frames. A target can take many
nights. Its status counts, for each sequence, the frames that are
archived and the frames that wait to be copied from the camera to
the archive.

The operator chooses an eligible target that is above the
telescope's horizon. The manual's page on targets gives the rules of
that choice. The operator drives the equipment through the
telescope's INDI server and PHD2. Then it moves each frame to the
target's archive, which is a volume claim that you choose, such as a
network share. The operator names each frame the way the target asks.
It checks the frame's SHA-256 after the copy, and records the frame in
a catalog of JSON lines beside the frames. A frame in the archive
never changes. The operator does not stack or develop frames, because
the archive is where other tools take them over. The plans for more
work, such as calibration frames, are in [`plans/`](plans/).

The operator works on a telescope that
[`observatory-operator`](../observatory-operator/) runs. It acts only
during a `Reservation` whose `spec.holder` is
`astrophotography-operator`, and only when the reservation names an
outbox. The outbox is the claim where the camera writes each frame
first, close to the camera.

To center a target, a `Solver` needs a claim that contains
astrometry.net's index files. You fill that claim yourself, so a
night needs no network access. The solver, and the workers that
extract the stars of a frame, run on the
[`astrometry`](../astrometry/) image. `observatory-operator` runs with
or without this operator.

## The kinds

- `Target` is an object to image: its coordinates, its sequences of
  filter, exposure, and count, its priority, its optical train, and
  its archive, with the file name of each frame.
- `Solver` is a plate solver that a target selects for centering. It
  names the claim of its index files, and the operator runs one
  solver pod for it.

The operator also runs a worker beside each reservation's outbox,
which finds the stars of a centering frame, and a promotion `Job`,
which moves a target's frames from the outbox to the archive.

## An example

```yaml
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Target
metadata:
  name: m42
spec:
  object: M42
  coordinates: {ra: "05:35:17.3", dec: "-05:23:28"}
  opticalTrain: east-imaging
  sequences:
    - {filter: Luminance, exposure: 300s, count: 60}
    - {filter: Red, exposure: 300s, count: 20}
  dither: {every: 2, pixels: 5}
  centering: {solver: example, tolerance: 30}
  archive:
    claimName: archive
    path: astro
    format: "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}"
```

[`examples/first-session.yaml`](examples/first-session.yaml) is a
whole session on the INDI simulators of `observatory-operator`'s
example observatory.

The manual is at
[liken.sh/astrophotography](https://liken.sh/astrophotography/), and
[`plans/00-design.md`](plans/00-design.md) is the design.
