# 75, Scene-level cast appearances

This is a plan for later. Nothing here is built. It records an
experiment from a 2026-09-30 conversation, which found that the
library can learn which credited person is on screen at each moment of
a title, at a cost a home cluster can pay. The first step writes that
answer to files on the library's volume. A feature that a person sees
on a screen comes after the files exist.

## The problem

The catalog records who is in a title, and not when. The credits fact
lists the cast of a movie and of each episode, and `.contributors/`
holds each person's headshot. Nothing records which of those people
appears in which scene. Amazon's X-Ray shows that answer on pause, and
it comes from IMDb's own data, which has no public API.

No public dataset fills the gap. TMDB, TVmaze, and the IMDb datasets
stop at the episode: a guest-star list with no time. TheIntroDB has
times, but only for the intro and the credits. The research datasets
that tag faces by scene, such as MovieNet (about 1,100 films) and
MovieGraphs (about 50), cover few titles and carry research licenses.
So the library computes the answer from its own files.

The problem is narrow, and the narrowness makes it cheap. The question
is not "who is this face" among everyone alive. It is "which of these
10 to 30 credited people is this face, if any", with a headshot for
each of them already on the volume.

## The experiment

**The setup.** One workstation with an Intel Core Ultra 7 165H
(Meteor Lake, 22 threads), its Arc integrated GPU, and one Coral USB
Accelerator. The film is *Charade* (1963), which is in the public
domain: 7,010 seconds of H.264 at 854x480 from the Internet Archive.
The gallery is seven cast headshots, the lead image of each actor's
Wikipedia article.

**The pipeline.** ffmpeg reads the video. A detector finds the faces
in each frame, and five landmarks per face align the face to a
112x112 crop. An embedding model turns each crop into a 512-dimension
vector. Each vector matches the nearest headshot vector by cosine
similarity, and a similarity of 0.35 or more names the person. The
detector is SCRFD (`det_500m`, 2.5 MB) and the embedding model is
MobileFaceNet (`w600k_mbf`, 13.6 MB), both from InsightFace's
`buffalo_s` pack. onnxruntime ran them on the CPU, and its OpenVINO
provider ran them on the Arc GPU. On the Coral, the detector is
Coral's own `ssd_mobilenet_v2_face_quant_postprocess_edgetpu.tflite`
(6.7 MB).

**Decode.** ffmpeg with `-skip_frame nokey` decodes only the
keyframes, and the encoder places a keyframe at most scene cuts. This
film has 1,563 keyframes, about one every 4.5 seconds, so the sample
is close to one frame per shot.

| Decode of the whole film | Wall | CPU time |
|---|---|---|
| Every frame, software, 4 threads | 115 s | 300 s |
| Every frame, VA-API on the Arc | 62 s | 55 s |
| Keyframes only, software | 15 s | 14 s |

**Detection and embedding.** The two runs below found the same 2,027
faces in the 1,563 keyframes. Each named about 580 of them.

| Run | Wall | CPU time | Peak memory | Detect | Embed |
|---|---|---|---|---|---|
| CPU, onnxruntime | 210 s | 4,300 s | 650 MB | 65 ms per frame | 49 ms per face |
| Arc GPU, OpenVINO | 41 s | 253 s | 1.6 GB | 18 ms per frame | 5.5 ms per face |

onnxruntime used all 22 threads in the CPU run, and most of its 4,300
seconds were threads that waited. The cost on one or two cores was not
measured.

**The Coral.** The Coral ran detection only, because nobody publishes
an Edge TPU build of a face-embedding model. The score below counts
the 525 keyframes in which the CPU pipeline named someone, and asks
whether the Coral found at least one face in each.

| Coral input | Per frame | Whole film | Found |
|---|---|---|---|
| The frame resized to 320x320 | 8 ms | 14 s | 436 of 525 |
| Two square tiles of the frame, each resized to 320x320 | 16 ms | 27 s | 523 of 525 |

A 16:9 frame resized to a square distorts each face, and the detector
misses many of them. Two overlapping square tiles keep the
proportions. The tiled run held 165 MB at its peak and used 26
seconds of CPU time. The same model in software, on the CPU, took 23
ms per frame.

**Precision.** Twelve random matches per actor, 84 in all, checked by
eye: 83 are the right person, and one is doubtful. The gallery held
headshots from other decades, such as a photo of George Kennedy taken
long after 1963, and the matches held.

**Recall.** Recall depends on the size of the face. The match rate by
the height of the detected face:

| Face height | Faces | Named |
|---|---|---|
| Under 40 px | 272 | 0% |
| 40 to 80 px | 494 | 12% |
| 80 to 160 px | 809 | 31% |
| 160 px and over | 452 | 60% |

A face in profile, a face in shadow, and an uncredited extra account
for most of the unnamed faces at large sizes. The experiment had no
labeled ground truth, so these numbers do not separate a miss from an
extra.

**Software for the Coral.** The USB Coral needs no kernel driver.
`libedgetpu` drives it through libusb. Google's apt repository and
`pycoral` stopped at older systems (`pycoral` supports Python 3.9 and
earlier), so the experiment used the builds that
[feranick](https://github.com/feranick/libedgetpu) maintains:
`libedgetpu` 16.0 built against TensorFlow 2.19.1, and `tflite_runtime`
2.17.1 for Python 3.12. The version mismatch did not matter, because
the delegate uses TensorFlow Lite's stable C interface.
`libedgetpu.so` is 1.2 MB and links only libc, libstdc++, libusb, and
libudev. `tflite_runtime` is 8.8 MB. The Coral changes USB identity
when the runtime loads its firmware, from `1a6e:089a` to `18d1:9302`,
and a non-root user needs a udev rule that covers both identities.

## The design

### Step 1: files on the volume

The design rule from [`00-design.md`](00-design.md) applies: the
volume holds every fact, and the catalog derives from the volume
alone. The first step is a fact that writes its answer to files beside
the title, and to nothing else. A lost catalog rebuilds the
appearances by a walk, and no video opens again.

**Two outputs.** The passes cost different amounts. The
expensive pass is decode, detection, and embedding: it opens the video.
The cheap pass is the match: it compares vectors with headshots. A new
headshot, a new credit, or a new threshold needs only the cheap pass.
So the fact keeps the output of each pass on the volume.

- **The faces record** holds one entry per detected face: the time of
  its keyframe, its box, the detector's score, and its embedding as
  16-bit floats. A header names the embedding model and the hash of
  its weights. This film's faces record would be about 2 MB, which is
  2,027 faces of 512 values at 2 bytes. The format must be one that a
  person can read with common tools. The builder chooses the format.
- **The ledger** is `.liken/appearances.yaml` in the title's folder,
  which follows the shape of `.liken/marks.yaml`. It is keyed on the
  file's path, and holds one entry for each face it named: the time,
  the `.contributors/` entry, and the similarity. It also holds the
  attempts, as every fact's ledger does. The ledger records the
  similarity, not only the name, so a later threshold change needs no
  new pass.

The ledger records observations at keyframe times, not spans. A span
from one keyframe to the next is an inference, and the reader of the
ledger can draw it.

**The gallery.** The credits fact's list in `.liken/` names each
credited person and their `.contributors/` entry. The headshot fact
puts a headshot in that entry. A person with no headshot cannot match,
and the ledger records that person as unmatched, so the gap is
visible. For an episode, the gallery is the series cast and that
episode's guest stars.

**The embedding model.** Two embedding models write
vectors that cannot be compared with each other. So every node embeds
with the same model, whatever its hardware, and the faces record names
that model. A change of model makes every faces record stale, and the
fact runs the expensive pass again. The detector can differ by node,
because a detector writes only boxes.

**Hardware.** The fact runs as a `Job` of its own, the way
trickplay does after [plan 58](completed/58-trickplay-on-the-gpu.md),
and claims its hardware through a `ResourceClaimTemplate` that the
`Library` names. Each step takes the best hardware the pod holds:

| Step | First choice | Then | Floor |
|---|---|---|---|
| Decode keyframes | VA-API | | Software, at about 15 s per film |
| Detect | Coral | Intel GPU or NPU, through OpenVINO | CPU |
| Embed | Intel GPU or NPU, through OpenVINO | | CPU |

A Coral is optional, because most clusters have none. An Intel GPU is
common but not universal. OpenVINO's CPU plugin also runs on AMD x86,
so the floor covers every machine that `liken` supports. A pod with a
Coral still embeds on its GPU or CPU.

**Scheduling.** The Job is per-title batch work, as trickplay is.
It never runs on a one-gigabyte screen machine. On the Arc it took
about a minute and 1.6 GB per film, once per file.

### Step 2: the catalog

The walk reads `.liken/appearances.yaml` into a catalog table, one row
per observation, deleted with its file and swept with it, the way the
`marks` table is. The walk never reads the faces record.

### Step 3: the user-facing feature

Later, and shaped in its own plan. The candidates are the cast on
screen when playback pauses, the scenes of one person from the
person's page, and a jump to the next appearance of a person. Each
needs only the catalog table.

## What was set aside

- **Trickplay sheets as the input.** They cost nothing to decode,
  because they already exist. They are 320 pixels wide at one frame
  every 10 seconds, so most faces fall below the size that matches. On
  this film they named 196 faces, against 581 from keyframes. They
  could answer "who is in this title", which the credits already
  answer.
- **Decoding every frame.** On VA-API it took four times as long as a
  software decode of the keyframes, and it adds frames from inside a
  shot, which mostly repeat faces already found.
- **A frame resized to the Coral's square input.** It missed 89 of the
  525 frames in which the CPU named someone. Two square tiles found 523.
- **An embedding model per backend.** Vectors from two models cannot
  be compared, so a library with mixed hardware would hold records that
  cannot be compared.
- **The catalog alone.** A faces table in SQLite only, with no file on
  the volume, would make the expensive pass a cost that every lost
  catalog pays again.

## Open questions

- **The CPU floor.** The cost of SCRFD and MobileFaceNet on one or two
  cores, with onnxruntime and no OpenVINO. That number decides whether
  a cluster with no accelerator runs this fact as a slow option or not
  at all.
- **Larger sources.** All the numbers here are from a 480p file. A
  keyframe of a 1080p or 4K file has many more pixels to decode. The
  detector and the embedding model see the same input sizes.
- **Ground truth.** A labeled set, even a few hundred faces by hand,
  to measure recall and precision instead of a spot check.
- **Recall from the film itself.** The confident matches of one film,
  used as a second gallery for the same film, would catch the same
  actor in the film's own lighting and makeup. Not tried.
- **The NPU.** Meteor Lake's NPU is present, and OpenVINO supports it.
  The workstation lacked its user-space driver, so the NPU was not
  measured.
- **The Coral as a claimed device.** This is a change in `liken`.
  `liken` publishes a USB device by its vendor and product, and a
  `DeviceClass` selects on those values. The Coral changes both when
  its firmware loads, and it gets a new device node. A claim for
  `1a6e:089a` would hold a node that disappears on the first run. The
  device publishing in `liken` needs a way to follow the
  re-enumeration, or to publish the Coral by a value that survives it.
- **The image.** The Coral's own runtime is about 17 MB with its
  model. onnxruntime with OpenVINO was 825 MB installed. The builder
  decides whether one image carries both, or each backend has an image
  of its own.

## The proof

Step 1 is proved on the lab. A Library with the fact on runs it on one
film and one episode, on a node with an Arc GPU and on a node with a
Coral. Both files are on the volume. A second run of the match alone,
from the faces record with the video not opened, writes the same
ledger. A spot check of the named crops shows the right people. The
run records the wall time, the CPU time, and the peak memory of each
step on each node.
