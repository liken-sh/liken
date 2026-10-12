# 01, Run a target's sequence

Stub. Proposed on 2026-10-12. Not designed.

A person declares a `Target`: an object, its coordinates, and its
sequences, each a filter, an exposure, and the number of frames
wanted. Given a `Reservation` that names this operator as its holder,
the operator images it: it slews the mount, starts guiding through
PHD2, dithers between frames, changes the filter, and exposes. The
person creates the reservation in this plan; the scheduler of [plan
04](04-the-scheduler.md) and the reservations of [plan
05](05-reservations-on-demand.md) come later.

An exposure, a dither, and a guide pulse happen too often to be
Kubernetes objects, so the operator drives them over INDI and PHD2's
API. The `Telescope`'s status names the INDI server, and each device's
status names its INDI device. A target relates to many reservations,
and its status keeps the progress across all of them.

Before each exposure, the operator sets the camera's upload directory
and file prefix inside the outbox of `observatory-operator` [plan
19](../../observatory-operator/plans/19-the-frames-outbox.md), and
the keywords it owns, such as the target and the frame's identity,
through the declared keywords of [plan
20](../../observatory-operator/plans/20-declared-fits-keywords.md). So
the driver writes each frame once, complete. The operator records each
frame in a `Catalog`, keyed by the frame's identity and its
reservation, because a night makes hundreds of frames and none of them
belongs in the API. The operator never asks the server for the frame's
bytes.

To design: the `Target` resource, how a frame's identity is formed,
and the frame record.
