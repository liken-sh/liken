# appearances

`appearances` finds which credited person is on screen at each second
of a title. It is step 1 of
[plan 75](../plans/75-scene-level-cast-appearances.md): the files on the
volume. The tool has three commands:

- `appearances detect <video>` decodes every frame of the video with
  `ffmpeg`, finds the faces with YuNet in every keyframe and in a frame
  every second, embeds each face with SFace, and writes
  `.liken/appearances/<video>.jsonl` beside the video. This is the
  expensive pass, and the only one that opens the video.
- `appearances match <title folder>` embeds the cast's headshots from
  `.liken/credits.yaml` and `.contributors/`, writes the gallery to
  `.liken/appearances/gallery.json`, and names the faces. For each video
  it writes `.liken/appearances/<video>.matches.json`, with each face it
  named, and `.liken/appearances/<video>.spans.json`, the file the liken
  display reads when the film pauses. Then it prints one line of JSON on
  standard output, a summary of the title. It takes 1 to 5 seconds for a
  film. The operator reads the summary and writes the ledger,
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
`liken.sh/appearances/detections/v2`, the video file's name, and its
`size` in bytes, read when `detect` starts. The library treats a file
whose size differs from its probe record as another file, so the
operator compares `size` with the probe record to tell whether the
detections still describe the file on the volume. The header also names
the detector and the embedder with the SHA-256 of each model file, the
device, the decoded frame size, and the video's length.

Each line after the header is one sample, with each face's box,
landmarks, score, and embedding, and a sample with no face is a line
with no faces. The samples are every keyframe and a frame every second
between them: the second count starts again at each keyframe. A keyframe
line carries `"keyframe": true`, and other lines leave the field out:

    {"time":5.672,"keyframe":true,"faces":[]}
    {"time":6.673,"faces":[{"x":402.1,"y":96.3,"w":88.0,"h":112.5,...}]}

Encoders place a keyframe at most cuts, and the spans use the keyframes
as the places where a shot can change. The flag is on each line, and not
a list in the header, because `ffmpeg` reports a keyframe only when the
decode reaches it. The header is written before the decode starts and
each line as its frame arrives, so a list in the header would need the
whole decode in memory or a second pass over the file. Every keyframe is
also a sample, so the flags list every keyframe.

The record holds every face that YuNet scored at 0.8 or more. At
FaceDetectorYN's default of 0.9, the detector found no face in two
thirds of the keyframes of films that are mostly people talking. The
faces scored from 0.8 to 0.9 were real faces in profile, in shadow, or in
a helmet. Under 0.8 the detections included equipment and the backs of
heads.

`match` reads only this format. The operator reads a record of any other
format as stale and decodes the video again.

### The cost of a sample every second

A keyframe stands for up to 10 seconds, and inside that time people
turn, enter, and leave. A frame between keyframes needs the frames it
references, so a sample every second needs a decode of every frame.
On the bench of nine films, on an Intel laptop iGPU through VA-API:

| Film | Source | Samples | Faces | Detect pass | Peak memory |
|---|---|---|---|---|---|
| 1976 | 1080p | 7,491 | 18,426 | 165 s | 412 MB |
| 1989 | 1080p | 7,997 | 17,483 | 214 s | 413 MB |
| 1992 | 1080p | 6,174 | 6,294 | 90 s | 393 MB |
| 1993 | 1080p | 6,689 | 7,798 | 171 s | 392 MB |
| 1996 | 4K HEVC | 7,239 | 8,532 | 367 s | 460 MB |
| 2005 | 1080p | 6,445 | 6,672 | 148 s | 389 MB |
| 2006 | 1080p | 9,262 | 13,184 | 242 s | 414 MB |
| 2015 | 1080p | 10,162 | 9,842 | 132 s | 388 MB |
| 2017 | 1080p | 7,556 | 6,903 | 93 s | 386 MB |

Each peak is the sum of the resident memory of the tool and `ffmpeg`,
read every 0.2 seconds. The keyframes alone took 8 to 26 seconds for the
same films. In software, on the two decode threads `--decode-threads`
sets by default and with the models on one CPU thread, the 4K film
decoded at 3.3 times its running speed, which is 34 minutes for the
film, and the tool and `ffmpeg` held 740 MB together.

A sample every 2 seconds costs nearly the same, because the decode is
the fixed cost, and showed 5 points less of the clear faces. Decoding
every I-frame (`-skip_frame nointra`) found 0 to 16% more frames than the
keyframes and named no more. These files are HEVC, and their P-frames
reference B-frames, so a decode that skips the B-frames returns damaged
pictures.

## Naming a face

`match` names faces in two passes. The headshot pass compares each
face's vector with each person's headshot vector by cosine similarity. A
face is named for the closest person when two conditions are true:

- The similarity is at least the threshold, 0.363, the value OpenCV
  publishes for SFace. `--threshold` sets another value.
- The similarity leads the next closest person's similarity by at least
  the margin, 0.05. `--margin` sets another value. A face as close to
  two headshots as to one is not evidence for either person.

On a 1080p and a 4K film, the margin of 0.05 removed 2 and 3 names of
about 780 and 650. Of the 5 names it removed, 2 were wrong when checked
by eye. The margin does not remove a wrong name with a large lead, such
as a face in heavy makeup named for another actor.

A headshot is one photograph, often decades from the film, and on the
bench the older the film, the fewer faces its headshots named. So the
film pass, `knn-guard`, names a face that the headshot pass left unnamed
from the faces of the same film that the headshot pass named. The face
takes a name only when three conditions are true:

- At least 2 faces that the headshots named, from other samples, have a
  similarity of 0.5 or more to it. A face of the same sample is another
  person in the same picture, so it does not count.
- All of those named faces name one person.
- The named faces are at least 30% of all the faces at 0.5 or more to
  it, named or not. A group of similar faces that the headshots almost
  never name, such as a character in prosthetic makeup, takes no name
  from the few of its faces that the headshots named wrong. Without this
  condition, a plain vote named one character's faces for another actor.

The film pass reads the headshot names only. A name it gives is never
evidence for another face, so one wrong name cannot spread through a
chain of similar faces. Of 1,085 of its names checked by eye on the
bench, 16 were wrong, 1.5%.

## The matches file and the summary

`match` writes one matches file for each video, whole, beside its
final name, and renames it into place:

```rust
pub struct Matches {
    pub format: String,          // "liken.sh/appearances/matches/v2"
    pub video: String,           // the video file's name in the title's folder
    pub size: u64,               // the detections record's size
    pub embedder: Model,         // { name, sha256 } of the SFace file
    pub threshold: f32,
    pub margin: f32,
    pub gallery: Vec<GalleryInput>,
    pub unmatched: Vec<Unmatched>,
    pub observations: Vec<Observation>,
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

pub struct Observation {
    pub time: f64,               // the sample's time in seconds
    pub face: usize,             // the face's place in its sample's line
    pub contributor: String,
    pub by: By,                  // "headshot" or "film", the pass that named the face
    pub similarity: f32,         // to this person's headshot
    pub runner_up: Option<f32>,  // the closest other person's similarity; absent in a gallery of one
}
```

A film's matches file is about 110 bytes per face named: from 390 KB to
810 KB on the bench. It is one JSON document, as the spans file is. A
JSON Lines file would let a reader stream the faces, but
`.liken/appearances/` holds the detections records as `.jsonl` files,
and `match` reads every `.jsonl` file there as a record.

`match` then prints one summary for each title folder, on one line:

```rust
pub struct Summary {
    pub format: String,          // "liken.sh/appearances/summary/v1"
    pub embedder: Model,
    pub threshold: f32,
    pub margin: f32,
    pub gallery: Vec<GalleryInput>,
    pub unmatched: Vec<Unmatched>,
    pub files: BTreeMap<String, FileSummary>, // by video file name
}

pub struct FileSummary {
    pub size: u64,               // the detections record's size
    pub named: Named,            // { headshot, film }: the faces each pass named
    pub people: usize,           // the different people the faces name
}
```

The operator writes the summary, not the faces, into the ledger. The
walk reads every ledger of a folder on each pass, and the faces of one
film made a ledger entry of about 680 KB, against about 5 KB for the
summary.

`gallery` lists every credited actor in billing order. The operator
compares each `sha256` with the headshot file to see that a headshot
changed, and compares each file's `size` with the probe record to see
that the video changed. Either change means the answer is stale.

`match` leaves out a detections record that another embedder wrote,
because its vectors cannot be compared with the gallery's, and names it
on standard error. A season folder holds one record per episode, so one
stale record does not stop the match of the others. The operator reads
a file with no part in the summary as no answer, and runs detect again
on it.

The matches file records `similarity` and `runner_up` to three
decimals, so a reader can apply a higher threshold or margin to the
faces `by` `headshot` without running `match` again. A face `by` `film`
took its name from other faces, so its `similarity` to the headshot can
be under the threshold, and its `runner_up` can be higher than its
`similarity`.

`match` still reads `.liken/credits.yaml`, which the credits fact
writes, so the crate keeps `serde_yaml_ng`. The tool reads the cast from
the volume, the same source the operator reads, and a review on a
workstation needs no operator to supply it.

## The spans file

The liken display shows the credited people on screen when a film
pauses. It reads finished spans and does no matching, merging, or
ordering of its own, so `match` writes one file per video in this shape:

    {
      "format": "liken.sh/appearances/spans/v1",
      "released": "2009-10-10",
      "people": {
        "jo/john-cusack": {
          "name": "John Cusack", "character": "Jackson Curtis",
          "portrait": "jo/john-cusack/headshot.jpg", "born": "1966-06-28"
        }
      },
      "spans": [
        {"start": 2993.991, "end": 2995.284,
         "people": ["jo/john-cusack", "am/amanda-peet", "to/tom-mccarthy"]}
      ]
    }

A span is a run of samples that name the same people. The rules:

- Each sample stands for half the gap to the sample before it and half
  the gap to the sample after it. Its stretch stops at the next
  keyframe, because a keyframe is where a cut can fall, and a stretch
  that crossed one would put the people of one shot over the next.
- The detector and the match miss faces turned away, in shadow, or too
  small to name, so a scene breaks into pieces. The people named last
  hold through samples that have faces but name nobody, for at most 10
  seconds after the end of their last sample.
- A single sample with no face at all, such as a cut to a hand or a
  door, holds the people the same way. Two samples in a row with no face
  end the span, because a wide shot with nobody in it ends the scene's
  people.
- Two spans of the same people that meet join into one. Samples before
  the first person named, and after a hold ends, are no span.

A span lists its people left to right by the average place of their
faces across its samples, so the cards read in the order the people
stand in the picture.

On the bench of nine films, 40 pause moments each labeled by hand with
the credited people in the shot, the spans of `match` scored:

| Films | Precision | Clear faces shown | All faces shown | Exactly right |
|---|---|---|---|---|
| All nine, 360 moments | 0.87 | 0.84 | 0.60 | 0.57 |
| One film, lowest to highest | 0.74 to 1.00 | 0.62 to 0.96 | 0.38 to 0.73 | 0.33 to 0.78 |

Precision is the share of the people shown who are in the shot. The
keyframes alone, with the headshot pass only and the earlier rules,
scored 0.75, 0.41, 0.28, and 0.36. A face seen from behind or in a
helmet does not match, so the share of all faces shown stays near 0.60.

Each person is keyed by their entry's path under `.contributors/`, and
`portrait` is a path under the same directory. `released` is the
`<premiered>` date of the video's own `.nfo`, or of `movie.nfo`, and
`born` and `died` come from the entry's `contributor.yaml`. The file
holds dates and not ages, because an age changes every year and the
display works it out against its own clock. A field with no value is
left out.

The file is written whole on every match, so it follows the latest
gallery and the latest rule.

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
or to `--out`. It names the faces with both passes, as `match` does.
`--threshold` and `--margin` work as they do for `match`, with no model
and no pass over the video. Its spans are not the display's: each
sample stands until the next one, and a run that names nobody is a
chapter of its own, so each gap in the naming shows.

`--play` opens `mpv` with the chapters and the overlay script:

- Each span is a chapter. The seek bar shows a tick at each span, and
  Page Up and Page Down move between spans.
- The top left corner shows the current span's times and people, and the
  sample the boxes come from.
- A green box is a named face. A yellow box is an unnamed face within
  0.1 of the threshold, or over the threshold but under the margin. A
  red box is a face nobody in the gallery is close to. Each label gives
  the closest person and the similarity.
- `k` and `j` seek to the next and the previous sample with a face, at
  its exact time, where each box sits on its face. Later the outline
  thins, because the faces move. `v` shows and hides the overlay.

`--sheets N` also writes contact sheets, each with up to N faces:

- For each person, the faces named as that person, with the weakest
  similarity first.
- `_closest leads`: the faces the headshot pass named whose person leads
  the next person by the least, which is where the margin decides.
- `_film <person>`: the faces the film pass named, with the weakest
  similarity to the headshot first. A wrong name of that pass shows
  here.
- `_unnamed`: the tallest faces nobody was named for.

A text file beside each sheet gives each tile's time, similarity, the
next person's similarity, and face height.
