# 19, The frames outbox

Proposed on 2026-10-12, and designed and built on 2026-10-12. Drilled
on the QEMU dev cluster on 2026-10-12, with INDI simulators; [the
drill](#the-drill) records the results. The design below answers what
the design conversation left open.

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

## The drill

The drill ran on 2026-10-12 on the QEMU dev cluster, with two guests:
`node-1`, a leader with 2 GB of memory, and `node-4`, a follower with
3 GB. The operator was built from 1f01d28d and reached the lab
through the lab registry as `ghcr.io/liken-sh/observatory-operator:dev`.
The observatory was a copy of `examples/simulators.yaml`, and
`Reservation east-tonight` named the outbox `frames`, a `local-path`
claim of 1Gi with the access mode `ReadWriteOnce`.

The drill deleted `Guider east` after the first check. The guider's
pod pulls the `weston` image from ghcr.io, about 270 MB, and the
laptop's link was too slow for that download. With no `Guider`, the
telescope has no guide camera, so `Camera east-guide` is an imaging
camera and mounts the outbox too, as the design says.

| Check | Result |
|---|---|
| Reserve `east` with `Guider east`, and read the pods during `Activation`, before `StartGuider` pulls weston | `east-main-camera` mounted `frames` at `/outbox`, with `fsGroup: 1000` and `fsGroupChangePolicy: OnRootMismatch`. `east-guide-camera` had only its `tmp` volume and no pod security context. The drill then ended the reservation, and no guider pod started. |
| Reserve `east` with no `Guider` | `Ready` 81 s after creation. `status.session.outbox` was `{claimName: frames}`. Both camera pods mounted `frames` at `/outbox` with `fsGroup` 1000, and no other device pod mounted it. |
| Read the main camera's upload settings through a port forward to `east-telescope` | `UPLOAD_DIR` was `/outbox`, `UPLOAD_PREFIX` was the driver's default `IMAGE_XXX`, and `UPLOAD_MODE` was `UPLOAD_BOTH`. |
| Take two exposures of 1 s | The client received each frame as a BLOB 1.6 s after the request: 2,629,440 bytes of FITS, 1280 by 1024 pixels of 16 bits. The driver reported `Image saved to /outbox/IMAGE_001.fits`, and then `IMAGE_002.fits`. |
| Read the claim from a `busybox` pod that mounts `frames` read-only | Two files of 2,629,440 bytes, owned by user and group 1000, with the mode `0644`. The root of the volume was owned by root and group 1000, with the mode `2777`, so the kubelet had set the group from `fsGroup`. Each header held `INSTRUME = 'CCD Simulator'`, `EXPTIME = 1`, `CCD-TEMP = -10`, and `FILTER = 'Luminance'`. |
| Change `spec.outbox.claimName` to `frames-elsewhere` while the reservation is `Ready` | 30 s later, both camera pods had the same UIDs and a `restartCount` of 0, and `status.session.outbox` still named `frames`. |
| End the reservation | Released 76 s after the delete, and 63 s of that was the camera's warm-up. A new read-only pod found both files with the same sizes. |
| Reserve `east` with the outbox `nope`, a claim that does not exist | `Failed` 10 s after creation. The summary of `PowerOn` was "missing PersistentVolumeClaim nope in namespace observatory, which spec.outbox.claimName names", and no `east-telescope` pod started. |
| Create the claim `nope`, and annotate `observatory.liken.sh/retry=1` | A `Retry` `Event`, "Retrying PowerOn", and `Ready` 52 s after the annotation. The operator removed the annotation, `status.session.outbox` named `nope`, and `east-main-camera` mounted `nope`. |

In the reservation with no `Guider`, the steps took 27 s for
`StartSite`, 4 s for `PowerOn`, 15 s for `StartDevices`, 2 s for
`Connect`, 1 s for `Configure`, and 31 s for `Activation`, most of
which was the camera's cooling to -10 °C.

After the edit of `spec.outbox.claimName`, the reservation's
`metadata.generation` was 2 and its `status.observedGeneration` stayed
1. The runner writes a reservation's status when a step changes, and
a `Ready` reservation runs no step, so `observedGeneration` stays
behind after any edit of the spec, not only an edit of the outbox.

The first reservation with no `Guider` failed on `node-1`, for a cause
outside this plan. The kubelet evicted every pod of the observatory
for low ephemeral storage, with 96 MB free against a threshold of
100 MB, and `Activation` failed when the camera's connection ended.
On these guests, `/var/lib/rancher` is a 1.9 GiB partition of the
state disk, and it holds the k3s data, etcd, and the images. The
kubelet's image collection then deleted the `indi-simulators` image
from `node-1`. The drill put a `NoSchedule` taint on `node-1`, made
the claim `frames` again so that it bound on `node-4`, and ran the
other checks on `node-4`. There, the same partition kept 488 MiB free
while the telescope was `Ready`. Apart from those evictions and the
expected failure of `PowerOn`, the drill saw no `Warning` `Event`. The
log of the operator pod on `node-4` held no error, except the two INDI
connections that ended when `PowerOff` and `StopSite` stopped their
servers.

## Still open

Two telescopes whose reservations name the same claim both write to
its root, and each driver numbers its files from the ones it finds.
Two drivers that write at the same moment can choose the same name.
`astrophotography-operator` sets its own directory and a unique name
for each frame, so only a person who shares one claim between two
desktop sessions meets this.
