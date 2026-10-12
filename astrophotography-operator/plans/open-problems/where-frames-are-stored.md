# Where frames are stored

Open problem. In mode 2 the camera writes each frame to a directory
with `UPLOAD_LOCAL`, and grading and plate solving read it there.
`observatory-operator` gives a device pod only a `tmp` `emptyDir` on a
read-only root filesystem, and it sets no `UPLOAD_MODE` or
`UPLOAD_DIR`. So no frame has a place to land yet.

Three questions depend on the answer:

- Which component gives the camera its frames volume, and what kind of
  volume it is. The camera's pod belongs to `observatory-operator`, and
  the frames belong to imaging.
- Where grading and the plate solver run. They read every frame, so
  they belong near it. A container in the camera's pod puts an
  `astrophotography-operator` concern inside an `observatory-operator`
  pod, and the two operators would need an agreed way to add one.
- How a stacking `Job` on a machine with more cores reads the frames
  from the node at the pier: through shared storage, or from a copy of
  a per-node volume.
