# 03, Promotion to the archive

Stub. Proposed on 2026-10-12. Not designed.

A frame written straight to a network share can make the next exposure
wait, and that wait repeats all night. So the camera writes to the
reservation's outbox, close to the camera, and frames move to the
long-term archive afterward.

Each `Target` names its archive claim and the file name format, which
is the person's to choose. The operator may add a suffix to make a name
unique. A promotion `Job` copies each frame to the archive, verifies
the copy against the checksums of the FITS checksum convention, and
only then removes the frame from the outbox. A crash at any point
leaves each frame in at least one verified place, and a second run
does no harm, because each frame has its own identity. The person sets
the pacing: during the night, or after it. When the outbox and the
archive are the same claim, no promotion runs.

A frame never changes after promotion. What the operator learns later,
such as a grade, is a record keyed by the frame's identity.

The operator puts a finalizer on the outbox claim, so that deleting
the claim waits until every frame in it is promoted. Uninstalling the
operator has to remove that finalizer.

To design: the fields on the `Target`, the pacing, and what the
operator does when the outbox nears full because the archive cannot be
reached.
