# 19, The frames outbox

Stub. Proposed on 2026-10-12. Not designed.

A camera driver writes each frame to a directory when its upload mode
is `UPLOAD_LOCAL`, and sends only the file's path to its clients. Today
a device pod has only a `tmp` `emptyDir` on a read-only root
filesystem, and the operator sets no upload mode, so a frame has no
durable place to land.

A `Reservation` names a claim, the outbox. The operator mounts it in
the telescope's camera pods and points each camera's upload directory
into it, so each frame is written once, on the machine at the pier,
with no copy over the network while the night runs. The claim is the
user's: it can be a volume on the pier machine or a network share.
The pods and the reservation start and stop together, so the mount
lasts exactly as long as the night's use of the telescope.

In mode 2, `astrophotography-operator` creates the reservation, names
the outbox, and later moves each frame to its long-term archive. In
mode 1, a person who reserves the telescope for KStars can name an
outbox too.

To design: the field, how the claim reaches each camera pod, and what
the step summary reports when the claim cannot be mounted.
