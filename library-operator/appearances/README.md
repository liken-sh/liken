# appearances

`appearances` finds which credited person is on screen at each keyframe
of a title. It is step 1 of
[plan 75](../plans/75-scene-level-cast-appearances.md): the files on the
volume. The tool has three commands:

- `appearances detect <video>` decodes the video's keyframes with
  `ffmpeg`, finds the faces with YuNet, embeds each face with SFace, and
  writes `.liken/appearances/<video>.jsonl` beside the video. This is the
  expensive pass, and the only one that opens the video.
- `appearances match <title folder>` embeds the cast's headshots from
  `.liken/credits.yaml` and `.contributors/`, writes the gallery to
  `.liken/appearances/gallery.json`, and prints one line of JSON on
  standard output with each face it named. It takes about a second. The
  operator reads that line and writes the ledger,
  `.liken/appearances.yaml`, with its attempts, as it writes every other
  ledger in `.liken/`. `--credits FILE` names another credits file to
  read the cast from. The credits fact credits a series and not its
  episodes, so the match of a season folder names the series' file.
- `appearances review <title folder>` is a development tool. It runs no
  model and writes nothing into the title's folder.

## The runtime

The models run on OpenVINO, through its C API, on the CPU or on an Intel
GPU. The binary opens `libopenvino_c.so` when it starts and links nothing
from OpenVINO at build time, so `cargo build` needs no OpenVINO
installation.

To run `detect` or `match` on a workstation, unpack Intel's runtime
archive and point the loader at it:

    curl -LO https://storage.openvinotoolkit.org/repositories/openvino/packages/2026.4.1/linux/openvino_toolkit_ubuntu24_2026.4.1.22982.07f9c262b05_x86_64.tgz
    tar xzf openvino_toolkit_ubuntu24_2026.4.1.22982.07f9c262b05_x86_64.tgz
    runtime=$PWD/openvino_toolkit_ubuntu24_2026.4.1.22982.07f9c262b05_x86_64/runtime
    export LD_LIBRARY_PATH=$runtime/lib/intel64:$runtime/3rdparty/tbb/lib

The archive's SHA-256 is
`772ce52d9b0aa375c77d391fccea71450d889c98b9af197bb8f05167a9dabd87`. The
GPU also needs Intel's OpenCL runtime, `intel-opencl-icd` on Debian and
Ubuntu.

The models are the files [OpenCV Zoo](https://github.com/opencv/opencv_zoo)
publishes, under their published names, in one directory that
`--models` or `$APPEARANCES_MODELS` names:

| File | License | SHA-256 |
|---|---|---|
| `face_detection_yunet_2023mar.onnx` | MIT | `8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4` |
| `face_recognition_sface_2021dec.onnx` | Apache 2.0 | `0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79` |

## The detections record

The first line of `<video>.jsonl` is the header. It names the format,
`liken.sh/appearances/detections/v1`, the video file's name, and its
`size` in bytes, read when `detect` starts. The library treats a file
whose size differs from its probe record as another file, so the
operator compares `size` with the probe record to tell whether the
detections still describe the file on the volume. The header also names
the detector and the embedder with the SHA-256 of each model file, the
device, the decoded frame size, and the video's length. Each line after
the header is one keyframe, with each face's box, landmarks, score, and
embedding.

## Naming a face

`match` compares each face's vector with each person's headshot vector
by cosine similarity. A face is named for the closest person when two
conditions are true:

- The similarity is at least the threshold, 0.363, the value OpenCV
  publishes for SFace. `--threshold` sets another value.
- The similarity leads the next closest person's similarity by at least
  the margin, 0.05. `--margin` sets another value. A face as close to
  two headshots as to one is not evidence for either person.

On a 1080p and a 4K film, the margin of 0.05 removed 2 and 3 names of
about 780 and 650. Of the 5 names it removed, 2 were wrong when checked
by eye. The margin does not remove a wrong name with a large lead, such
as a face in heavy makeup named for another actor.

`--self-gallery` is an experiment, and it is off by default. After the
first pass, each person's most confident faces in the film join the
gallery as extra vectors for that person: up to 10 faces from 10
different keyframes, each named with a similarity of at least 0.5. Then
every face is named again. A person's similarity is their closest
vector, and the margin compares people, not vectors. On the same two
films the experiment named 16% and 23% more faces, many of them in
helmets, in profile, and in makeup. It also added wrong names, about 1
in 10 of the faces it added in the contact sheets, so it is not the
default.

## The match document

`match` prints one JSON document for each title folder, on one line:

```rust
pub struct Matches {
    pub format: String,          // "liken.sh/appearances/matches/v1"
    pub embedder: Model,         // { name, sha256 } of the SFace file
    pub threshold: f32,
    pub margin: f32,
    pub self_gallery: bool,
    pub gallery: Vec<GalleryInput>,
    pub unmatched: Vec<Unmatched>,
    pub files: BTreeMap<String, FileMatches>, // by video file name
}

pub struct GalleryInput {
    pub contributor: String,     // ".contributors/ma/matt-damon"
    pub name: String,
    pub headshot: Headshot,      // "found", "missing", or "no-face"
    pub sha256: Option<String>,  // of the headshot file; absent when missing
}

pub struct Unmatched {
    pub contributor: String,
    pub name: String,
    pub headshot: Headshot,      // "missing" or "no-face"
}

pub struct FileMatches {
    pub size: u64,               // the detections record's size
    pub observations: Vec<Observation>,
}

pub struct Observation {
    pub time: f64,               // the keyframe's time in seconds
    pub face: usize,             // the face's place in its keyframe's line
    pub contributor: String,
    pub similarity: f32,
    pub runner_up: Option<f32>,  // the next person's similarity; absent in a gallery of one
}
```

`gallery` lists every credited actor in billing order. The operator
compares each `sha256` with the headshot file to see that a headshot
changed, and compares each file's `size` with the probe record to see
that the video changed. Either change means the document is stale.

`files` leaves out a detections record that another embedder wrote,
because its vectors cannot be compared with the gallery's, and the match
names it on standard error. A season folder holds one record per
episode, so one stale record does not stop the match of the others. The
operator reads a file with no part in the document as no answer, and
runs detect again on it. The
document records `similarity` and `runner_up` to three decimals, so a
reader can apply a higher threshold or margin without running `match`
again.

`match` still reads `.liken/credits.yaml`, which the credits fact
writes, so the crate keeps `serde_yaml_ng`. The tool reads the cast from
the volume, the same source the operator reads, and a review on a
workstation needs no operator to supply it.

## A review

Run the passes on a copy of a title folder, never on the library itself.
The copy needs the title's folder, with its `.liken/`, and the
`.contributors/` entries its credits name, at the same paths from a
common root.

    appearances detect --device gpu --cache ~/.cache/appearances "Movies/Sci-Fi/Film [2015]/Film [2015, Bluray-1080p].mkv"
    appearances match "Movies/Sci-Fi/Film [2015]"
    appearances review "Movies/Sci-Fi/Film [2015]" --sheets 24 --play

For an episode, the title folder is the season folder, and the cast is
the series':

    appearances match "Series/Show/Season 01" --credits "Series/Show/.liken/credits.yaml"

`review` writes the spans as YAML, the spans as an mpv chapters file, and
each face's box and label to a directory under `/tmp/appearances-review/`,
or to `--out`. `--threshold`, `--margin`, and `--self-gallery` work as
they do for `match`, with no model and no pass over the video.

`--play` opens `mpv` with the chapters and the overlay script:

- Each span is a chapter. The seek bar shows a tick at each span, and
  Page Up and Page Down move between spans.
- The top left corner shows the current span's times and people, and the
  keyframe the boxes come from.
- A green box is a named face. A yellow box is an unnamed face within
  0.1 of the threshold, or over the threshold but under the margin. A
  red box is a face nobody in the gallery is close to. Each label gives
  the closest person and the similarity.
- `k` and `j` seek to the next and the previous keyframe with a face, at
  its exact time, where each box sits on its face. Later in the shot the
  outline thins, because the faces move. `v` shows and hides the overlay.

`--sheets N` also writes contact sheets, each with up to N faces:

- For each person, the faces named as that person, with the weakest
  similarity first.
- `_closest leads`: the named faces whose person leads the next person
  by the least, which is where the margin decides.
- `_unnamed`: the tallest faces nobody was named for.
- With `--self-gallery`, `_new <person>`: the faces that only the second
  pass named, with the weakest similarity first.

A text file beside each sheet gives each tile's time, similarity, the
next person's similarity, and face height.
