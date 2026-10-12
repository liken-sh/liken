# 19, The frames outbox

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the field is not designed
yet.

## The problem

A camera driver writes each frame to a directory when its upload mode
is `UPLOAD_LOCAL`, and sends only the file's path to its clients. Today
a device pod has only a `tmp` `emptyDir` on a read-only root
filesystem, and the operator sets no upload mode, so a frame has no
durable place to land. `astrophotography-operator` needs every frame
written once, close to the camera, without its bytes crossing the
network while the night runs.

## Decisions

- **The `Reservation` names the outbox, as the name of a claim.** The
  operator starts a telescope's pods only while a reservation is
  active, so the pods and the reservation start and stop together, and
  a volume that the reservation names is mounted for exactly as long
  as the night's use of the telescope. A person reserving the telescope
  for KStars in mode 1 can name an outbox too.
- **The operator mounts the claim in the telescope's camera pods and
  points each camera's upload directory into it.** Each frame is
  written once, by the driver, already in place.
- **The claim is the person's.** It is an existing claim, not a
  template, so the frames persist from night to night in a place the
  person chose. It can be a volume on the machine at the pier, which
  costs no network hop while the night runs, or a network share. The
  operator does not prefer either. A claim that cannot be mounted
  fails the step that starts the pods, and the step says why.
- **The outbox is not the archive.** A frame written straight to a
  network share can make the next exposure wait, and the wait repeats
  all night. `astrophotography-operator` moves frames from the outbox
  to each target's archive afterward, and skips the move when the two
  are the same claim.
- **This operator knows nothing about file names or archives.** Where a
  frame ends up and what it is called belong to imaging.
  `astrophotography-operator` sets the directory and the file prefix
  inside the outbox before each exposure.

## Considered and set aside

- **The outbox on the `Camera`.** A camera's resource is inventory that
  outlives every night, and the place for a night's frames is a choice
  about that night.
- **A volume template for each reservation.** Each night's frames would
  land in a volume of their own, and something would have to gather
  them.

## Still open

The field, how the claim reaches each camera pod, and how a camera that
several telescopes share at different times finds the right outbox.
