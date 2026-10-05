# Where a stacking job reads frames

Open problem. The camera's driver writes each frame to a volume in the
camera's pod, on the node at the pier, and grading reads it there. A
Siril `Job` that stacks the accepted frames runs on a machine with
more cores, so it has to read frames from another node: through shared
storage, or from a copy of a per-node volume. Root plan 74 lists this
under "Open questions".
