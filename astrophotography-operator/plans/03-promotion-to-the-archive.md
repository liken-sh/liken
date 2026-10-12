# 03, Promotion to the archive

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the fields and the `Job` are
not designed yet.

## The problem

Frames are worth keeping for years, in a place the person chooses,
under names the person chooses. But a frame written straight to a
network share can make the next exposure wait, and that wait repeats
all night. So the camera writes to the reservation's outbox, close to
the camera, and the frames move to their long-term home afterward
without risk of loss on the way.

## Decisions

- **Each `Target` names its archive, as the name of a claim.** A
  target's frames belong together across every night it is imaged, so
  the archive belongs to the target, not to a night. The outbox belongs
  to the `Reservation` ([`observatory-operator` plan
  19](../../observatory-operator/plans/completed/19-the-frames-outbox.md)). When
  the two are the same claim, no promotion runs.
- **The person owns the file names.** A target states the format, and
  the operator may add a suffix only to make a name unique. Because the
  camera writes into the outbox under the final name, promotion copies
  without renaming.
- **Promotion is a `Job` that copies, verifies, and only then
  deletes.** It copies each frame to the archive, reads the copy back
  and checks it against the checksums of the FITS checksum convention,
  and removes the frame from the outbox only after the check passes. A
  crash at any point leaves each frame in at least one verified place,
  and a second run does no harm, because each frame has its own
  identity.
- **The person sets the pacing**: promote during the night, so the
  outbox stays small, or after it, so the link stays quiet while the
  telescope works.
- **A frame never changes after promotion.** What the operator learns
  later, such as a grade, is a record keyed by the frame's identity.
- **The operator puts a finalizer on the outbox claim**, so deleting
  the claim waits until every frame in it is promoted. Uninstalling the
  operator removes that finalizer.

## Considered and set aside

- **A finalizer on the camera's pod.** It keeps the pod's object, but
  the kubelet still stops its containers, and the frames are on the
  claim, which outlives the pod anyway.
- **INDI writing straight to the network share.** It costs a wait on
  every exposure.

## Still open

The fields on the `Target`, the pacing's settings, and what the
operator does when the outbox nears full because the archive cannot be
reached. It has to stop exposing before the claim is full, and say so
in a condition.
