# 75, Scene-level cast appearances

This is a plan for later. Nothing here is built. It records
experiments from conversations on 2026-09-30 and 2026-10-01, which
found that the library can learn which credited person is on screen at
each moment of a title, at a cost a home cluster can pay. The first
step writes that answer to files on the library's volume. A feature
that a person sees on a screen comes after the files exist.

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

## The experiments

**The setup.** One workstation with an Intel Core Ultra 7 165H
(Meteor Lake, 22 threads), its Arc integrated GPU, and one Coral USB
Accelerator. The film is *Charade* (1963), which is in the public
domain: 7,010 seconds of H.264 at 854x480 from the Internet Archive.
The gallery is seven cast headshots, the lead image of each actor's
Wikipedia article.

**The pipeline.** ffmpeg reads the video. A detector finds the faces
in each frame, and five landmarks per face align the face to a
112x112 crop. An embedding model turns each crop into a vector. Each
vector matches the nearest headshot vector by cosine similarity, and a
similarity at or above a threshold names the person.

**Decode.** ffmpeg with `-skip_frame nokey` decodes only the
keyframes, and the encoder places a keyframe at most scene cuts. This
film has 1,563 keyframes, about one every 4.5 seconds, so the sample
is close to one frame per shot.

| Decode of the whole film | Wall | CPU time |
|---|---|---|
| Every frame, software, 4 threads | 115 s | 300 s |
| Every frame, VA-API on the Arc | 62 s | 55 s |
| Keyframes only, software | 15 s | 14 s |

### The OpenCV Zoo models

These are the models the design uses. The detector is YuNet
(`face_detection_yunet_2023mar.onnx`, 230 KB, MIT license). The
embedding model is SFace (`face_recognition_sface_2021dec.onnx`, 38.7
MB, Apache 2.0), a MobileFaceNet that writes a 128-value vector. Both
come from [OpenCV Zoo](https://github.com/opencv/opencv_zoo). The
threshold is 0.363, the cosine threshold that OpenCV publishes for
SFace. The `2026may` YuNet file holds the same weights with a dynamic
input shape, and it found the same faces.

| Run | Wall | CPU time | Peak memory | Detect | Embed |
|---|---|---|---|---|---|
| OpenCV DNN, CPU, one thread | 63 s | 78 s | 417 MB | 18 ms per frame | 15 ms per face |
| onnxruntime with OpenVINO, Arc GPU | 21 s | 131 s | 831 MB | 7.4 ms per frame | 3.9 ms per face |
| onnxruntime with OpenVINO, CPU plugin | 23 s | 171 s | 555 MB | 6.5 ms per frame | 5.3 ms per face |

The CPU run found 2,245 faces and named 801. The GPU run found 2,247
and named 802, because the GPU computes in 16-bit floats. The OpenVINO
CPU run matched the OpenCV run on every face. OpenVINO decoded YuNet's
raw outputs through a decoder written for the experiment, and it
matched OpenCV's `FaceDetectorYN` on 40 of 40 test frames.

The GPU run finished three times faster, but it used more CPU time
than the one-thread run. The table gives the GPU's first run, which
spent 17 seconds outside the film on startup, a 40-frame check, and
the compilation of the GPU kernels. A second run found those kernels
in OpenVINO's cache, took 23 seconds in all, and used 98 seconds of
CPU time. The OpenVINO CPU plugin used many threads although the
session asked for one.

**Precision.** Twelve random matches per actor, 84 in all, checked by
eye: 84 are the right person. The gallery held headshots from other
decades, such as a photo of George Kennedy taken long after 1963, and
the matches held.

**Recall.** Recall depends on the size of the face. The match rate by
the height of the detected face:

| Face height | Faces | Named |
|---|---|---|
| Under 40 px | 365 | 1% |
| 40 to 80 px | 569 | 18% |
| 80 to 160 px | 840 | 42% |
| 160 px and over | 471 | 73% |

A face in profile, a face in shadow, and an uncredited extra account
for most of the unnamed faces at large sizes. The experiments had no
labeled ground truth, so these numbers do not separate a miss from an
extra.

### The InsightFace models

The first experiment used InsightFace's `buffalo_s` pack: SCRFD
(`det_500m`, 2.5 MB) to detect and MobileFaceNet (`w600k_mbf`, 13.6 MB,
512 values) to embed, at a threshold of 0.35. The license of these
models is "non-commercial research purposes only", so `liken` cannot
ship them. The measurements stay here for comparison.

| Run | Wall | CPU time | Peak memory | Detect | Embed |
|---|---|---|---|---|---|
| onnxruntime, CPU, 22 threads | 210 s | 4,300 s | 650 MB | 65 ms per frame | 49 ms per face |
| onnxruntime with OpenVINO, Arc GPU | 41 s | 253 s | 1.6 GB | 18 ms per frame | 5.5 ms per face |

Both runs found 2,027 faces and named about 580. A spot check of 84
matches found 83 right and one doubtful. By face height, 0% of faces
under 40 px were named, 12% from 40 to 80 px, 31% from 80 to 160 px,
and 60% at 160 px and over. On every measure that both experiments
took, the OpenCV Zoo models did as well or better.

### The Coral

The Coral runs only int8 TensorFlow Lite models that the Edge TPU
compiler has compiled. Coral's own face detector,
`ssd_mobilenet_v2_face_quant_postprocess_edgetpu.tflite` (6.7 MB,
Apache 2.0), runs as published. The score below counts the 525
keyframes in which the InsightFace pipeline named someone, and asks
whether the Coral found at least one face in each.

| Coral detector input | Per frame | Whole film | Found |
|---|---|---|---|
| The frame resized to 320x320 | 8 ms | 14 s | 436 of 525 |
| Two square tiles of the frame, each resized to 320x320 | 16 ms | 27 s | 523 of 525 |

A 16:9 frame resized to a square distorts each face, and the detector
misses many of them. Two overlapping square tiles keep the
proportions. The tiled run held 165 MB at its peak. YuNet on one CPU
thread takes 18 ms per frame, so the Coral detects no faster than one
core.

No Edge TPU build of a face-embedding model is published, so the
experiment made two. Each model was rebuilt in Keras from its ONNX
weights, quantized to int8 with 400 crops from the film, and compiled.
Four defects of the toolchain or the Edge TPU appeared on the way:

- **The Edge TPU's `PRELU` applies the absolute value of the slope.**
  A channel with a negative slope comes out wrong, with no warning from
  the compiler. A model with only the first convolution and `PRELU` of
  MobileFaceNet matched the CPU exactly on every channel with a
  non-negative slope, and differed on every channel with a negative
  one. Across 34 layers the error made every face's vector nearly the
  same. The fix writes each `prelu(x; a)` as
  `prelu(x; a+) + a- * relu(-x)`, with `a+ = max(a, 0)` and
  `a- = max(-a, 0)`, which uses no negative slope.
- **The compiler failed on SFace's last layer**, a dense layer with
  50,176 inputs, with "Internal compiler error". The same map as a 7x7
  convolution compiled, but its 6.4 million weights did not fit in
  the Edge TPU's memory, and 7.6 MB of weights went over USB for each
  face.
- **The Edge TPU has no grouped convolution.** MobileFaceNet's one
  grouped convolution became a full convolution with a block-diagonal
  kernel.
- **The converters failed.** `onnx2tf` 1.29 and 2.4 both failed on
  the depthwise convolutions under TensorFlow 2.19, so the Keras
  rebuild was written by hand. TensorFlow 2.5, 2.13, and 2.19 gave the
  same compiled result.

| Embedding | Per face | Median cosine to float32 | Same name or no name | Named as the wrong person |
|---|---|---|---|---|
| `w600k_mbf` int8, Coral | 10 ms | 0.81 | 92% | 0 |
| SFace int8, Coral, last layer as a 7x7 convolution | 30 ms | 0.875 | 91% | 2 |
| SFace int8, Coral, last layer on the CPU | 20 ms | 0.875 | 91% | 2 |
| SFace int8, TensorFlow Lite, CPU, one thread | 55 ms | 0.97 | 97% | 0 |

With the last layer on the CPU, the rest of SFace takes 3.29 MiB of
the Edge TPU's memory, and the last layer is one 2.9 ms matrix
product. The int8 models named more faces than float32 at the same
threshold, because their similarities run higher. Of 40 such extra
names from `w600k_mbf`, 39 were right.

**Software for the Coral.** The USB Coral needs no kernel driver.
`libedgetpu` drives it through libusb. Google's apt repository for
Coral returns "Project ... has been deleted", and `pycoral` supports
Python 3.9 and earlier. The experiment used the builds that
[feranick](https://github.com/feranick/libedgetpu) maintains:
`libedgetpu` 16.0 built against TensorFlow 2.19.1, and `tflite_runtime`
2.17.1 for Python 3.12. The compiler is version 14.1, from the
[`google-coral/edgetpu`](https://github.com/google-coral/edgetpu)
repository. `libedgetpu.so` is 1.2 MB and links only libc, libstdc++,
libusb, and libudev. The Coral changes USB identity when the runtime
loads its firmware, from `1a6e:089a` to `18d1:9302`, and a non-root
user needs a udev rule that covers both identities.

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
  its keyframe, its box, its five landmarks, the detector's score, and
  its embedding as 16-bit floats. A header names the embedding model
  and the hash of its weights. This film's faces record would be about
  575 KB, which is 2,245 faces of 128 values at 2 bytes. The format
  must be one that a person can read with common tools. The builder
  chooses the format.
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

**The models.** YuNet detects and SFace embeds, as the ONNX files
OpenCV Zoo publishes, with no conversion. OpenCV's DNN module or ONNX
Runtime runs them, so the runtime needs no Python and no TensorFlow.
Their weights do not go into this repository. A workstation pushes them
to a registry as an OCI artifact, with each model's license file and
an attribution notice, and the image build pulls that artifact.

**One embedding model everywhere.** Two embedding models write
vectors that cannot be compared with each other. So every node embeds
with SFace in float32, whatever its hardware, and the faces record
names that model. The GPU's 16-bit arithmetic stays within that
model: the GPU run named 802 faces where the CPU named 801. An int8
build of SFace is a different model, because its vectors differ from
float32 (median cosine 0.875 on the Coral), and it would need a name of
its own. A change of model makes every faces record stale, and the
fact runs the expensive pass again.

**Hardware.** The fact runs as a `Job` of its own, the way
trickplay does after [plan 58](completed/58-trickplay-on-the-gpu.md).
The CPU is the default, and the GPU is an option, claimed through a
`ResourceClaimTemplate` that the `Library` names:

| Step | With a GPU claim | Without |
|---|---|---|
| Decode keyframes | VA-API | Software, about 15 s per film |
| Detect and embed | Intel GPU, through OpenVINO | CPU, one thread, about 60 s per film |

One CPU thread is enough, because the work is a minute of one core per
480p film, once per file. The GPU saves wall time and costs CPU time,
as the measurements show. The Coral is not part of step 1. It detects
and embeds no faster than one core, and its embeddings would need a
model of their own.

**Scheduling.** The Job is per-title batch work, as trickplay is.
It never runs on a one-gigabyte screen machine. On the CPU it held
417 MB.

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

- **InsightFace's models.** Their license allows non-commercial
  research use only. On this film, YuNet and SFace named more faces at
  every face size and cost less.
- **The Coral in step 1.** Its detector takes 16 ms per frame, against
  18 ms for YuNet on one CPU thread. SFace on the Coral takes 20 ms per
  face, against 15 ms in float32 on one CPU thread, and it needs the
  `PRELU` and last-layer workarounds and a model name of its own.
- **Trickplay sheets as the input.** They cost nothing to decode,
  because they already exist. They are 320 pixels wide at one frame
  every 10 seconds, so most faces fall below the size that matches. On
  this film they named 196 faces, against 581 from keyframes, with the
  InsightFace models. They could answer "who is in this title", which
  the credits already answer.
- **Decoding every frame.** On VA-API it took four times as long as a
  software decode of the keyframes, and it adds frames from inside a
  shot, which mostly repeat faces already found.
- **An embedding model per backend.** Vectors from two models cannot
  be compared, so a library with mixed hardware would hold records that
  cannot be compared.
- **The catalog alone.** A faces table in SQLite only, with no file on
  the volume, would make the expensive pass a cost that every lost
  catalog pays again.
- **The weights in this repository.** They are 39 MB of binary files
  that no build step changes, and the OCI artifact keeps them out of
  the repository's history.

## Open questions

- **Larger sources.** All the numbers here are from a 480p file. A
  keyframe of a 1080p or 4K file has many more pixels to decode, and
  YuNet runs on the whole frame. SFace sees the same 112x112 crops.
- **The training data.** OpenCV Zoo publishes YuNet under MIT and
  SFace under Apache 2.0, and `liken` relies on those licenses. YuNet
  was trained on WIDER FACE. The SFace paper trained on CASIA-WebFace,
  VGGFace2, and MS-Celeb-1M, which Microsoft withdrew in 2019, and the
  zoo does not say which data the published weights used. Whether a
  dataset's terms reach a model's weights is not settled.
- **Ground truth.** A labeled set, even a few hundred faces by hand,
  to measure recall and precision instead of a spot check.
- **Recall from the film itself.** The confident matches of one film,
  used as a second gallery for the same film, would catch the same
  actor in the film's own lighting and makeup. Not tried.
- **The GPU's CPU time.** The OpenVINO GPU run used 98 to 131 seconds
  of CPU time for about 21 seconds of work, against 78 seconds for the
  whole one-thread CPU run. Where it goes is not measured: the
  provider's own threads, the Python loop, and the copies to and from
  the GPU are the candidates. A runtime outside Python may cost less.
- **The NPU.** Meteor Lake's NPU is present, and OpenVINO supports it.
  The workstation lacked its user-space driver, so the NPU was not
  measured.
- **Denser sampling for tighter spans.** Keyframes come about every
  4.5 seconds, so the ledger places a person near a time, not at it.
  Sampling 1 or 2 frames per second gives 7,000 to 14,000 frames per
  film. On the Coral, detection at 16 ms per frame would take 2 to 4
  minutes and little CPU time. Faces could be tracked from frame to
  frame by the overlap of their boxes, so each track is embedded once
  and the extra frames only extend its span. The cost is a decode of
  every frame, 62 seconds on VA-API for this film. Not measured, and
  not part of step 1. This is the one use found for the Coral in this
  feature.
- **The Coral as a claimed device.** This matters only if the Coral
  returns. `liken` publishes a USB device by its vendor and product,
  and a `DeviceClass` selects on those values. The Coral changes both
  when its firmware loads, and it gets a new device node. A claim for
  `1a6e:089a` would hold a node that disappears on the first run.
- **The image.** OpenCV's DNN module and ONNX Runtime both run the
  models on the CPU. OpenVINO adds the GPU, and onnxruntime with
  OpenVINO was 825 MB installed. The builder decides whether one image
  carries the GPU runtime, or the GPU has an image of its own.

## The proof

Step 1 is proved on the lab. A Library with the fact on runs it on one
film and one episode, on a node with no GPU claim and on a node with
an Arc GPU claim. Both files are on the volume, and both runs name the
same faces within the GPU's 16-bit margin. A second run of the match
alone, from the faces record with the video not opened, writes the
same ledger. A spot check of the named crops shows the right people.
The run records the wall time, the CPU time, and the peak memory of
each step on each node.
