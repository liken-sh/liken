# 19, The frames outbox

Proposed on 2026-10-12, and designed and built on 2026-10-12. The
design below answers what the design conversation left open.

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

## The design

- **The field is `spec.outbox.claimName`** on the `Reservation`: the
  name of a `PersistentVolumeClaim` in the reservation's namespace, in
  the shape of a pod's `persistentVolumeClaim` volume. An object, and
  not a bare string, leaves room for a later field such as a
  `subPath`. A reservation with no outbox runs as it did before: the
  driver writes local frames to its `tmp` `emptyDir`, and the
  operator does not touch the upload settings.
- **The session records the outbox.** `PowerOn` copies the field into
  `status.session` with the devices, so an edit during a session
  takes effect at the next session, the same rule [plan
  17](completed/17-a-session-freezes-its-devices.md) gives a device. A
  claim cannot change under a running driver without a new pod, and a
  new pod in the middle of an exposure loses the exposure.
- **`PowerOn` checks the claim before it creates a pod.** A claim that
  does not exist fails the step, and the step's summary names the
  claim and the namespace. A claim that exists but cannot be mounted,
  such as a `ReadWriteOnce` volume bound on another node than the
  camera's, leaves the pod `Pending`, and the step that waits for the
  pod reports the scheduler's message.
- **Only the imaging cameras mount it.** A camera that a `Guider`
  guides with sends its frames to PHD2 and never writes a file, so its
  pod does not mount the claim. Every other `Camera` of the telescope
  mounts the claim at `/outbox`.
- **The pod's `fsGroup` is the driver's group.** The driver runs as
  user and group 1000 on a read-only root, and a new volume is often
  owned by root. The camera pod sets `fsGroup: 1000` with
  `fsGroupChangePolicy: OnRootMismatch`, so the kubelet gives the
  driver write access and walks a large volume only when its root
  does not match.
- **`Configure` points the driver at the outbox.** It writes
  `UPLOAD_SETTINGS` with `UPLOAD_DIR` set to `/outbox`, and leaves
  `UPLOAD_PREFIX` to the holder. It sets `UPLOAD_MODE` to
  `UPLOAD_BOTH`, so every frame lands in the outbox and a desktop
  client still receives it. The holder can change the mode and the
  directory afterward: `astrophotography-operator` chooses
  `UPLOAD_LOCAL` and a directory for each target, and Ekos applies its
  own upload settings when it captures.
- **A camera cannot be shared by two telescopes,** because each device
  has one parent and runs on one server at a time. So the question of
  which outbox a shared camera writes to does not come up.

## Still open

Two telescopes whose reservations name the same claim both write to
its root, and each driver numbers its files from the ones it finds.
Two drivers that write at the same moment can choose the same name.
`astrophotography-operator` sets its own directory and a unique name
for each frame, so only a person who shares one claim between two
desktop sessions meets this.
