# titles

`titles` finds the second that the end titles of a video start. The
up-next card rises there, and a play counts as finished there, so a
start that is a minute late keeps a person in front of a credit roll,
and one that is a minute early cuts the last scene. It is the tool of
[plan 96](../plans/96-end-titles-from-the-picture.md). The tool has
three commands:

- `titles read <video>` decodes the end of the video with `ffmpeg`, one
  frame a second, finds the lines of text in each frame, reads the text
  of a frame every 8 seconds, and writes
  `.liken/titles/<video>.jsonl` beside the video. This is the expensive
  pass, and the only one that opens the video.
- `titles train --labels FILE --out FILE` fits a small model on the
  readings of the videos that have a mark for their end titles already,
  measures it on videos it was not fitted on, and writes the model file.
  It opens no video and loads no network.
- `titles place <video>... --model FILE` prints, for each video, the
  second its end titles start and how sure the model is, from the
  video's reading and the model file. It takes well under a second for a
  video, so it runs again over every reading when the model changes.

Each cluster fits its own model from its own library. The repository
holds no model and no list of videos.

## The runtime

`read` runs two networks on OpenVINO, through its C API. The binary
opens `libopenvino_c.so` when it starts and links nothing from OpenVINO
at build time, so `cargo build` needs no OpenVINO installation, and
`train` and `place` run with none. To run `read` on a workstation,
unpack Intel's runtime archive and point the loader at it, as
[the `appearances` README](../appearances/README.md#the-runtime)
describes.

The networks run on the CPU by default. `--device gpu` runs them on an
Intel GPU, at 32-bit precision: at the GPU's default of 16 bits, the
text detector marked text in every second of a film where the CPU
marked it in 42% of them. At 32 bits the GPU writes the same reading as
the CPU, and it is no faster than two CPU threads for these two
networks, so the CPU is the default. `--hwaccel vaapi` decodes the
video on the GPU, which is where a GPU saves time. When the GPU cannot
decode or scale the video, `read` decodes the window again in software.

The models are in one directory that `--models` or `$TITLES_MODELS`
names. [`models/pin.toml`](models/pin.toml) gives the URL, the SHA-256,
and the license of each:

| File | What it is |
|---|---|
| `text_detection_en_ppocrv3_2023may.onnx` | PP-OCRv3's text detector, as OpenCV Zoo publishes it |
| `text_recognition_ch_ppocrv4.onnx` | PP-OCRv4's text reader, as RapidOCR publishes it |
| `text_recognition_ch_ppocrv4.txt` | The reader's characters, PaddleOCR's `ppocr_keys_v1.txt` |

## The reading

`read` decodes the last `--window` seconds of the video, 900 by
default. The end titles of 95% of the films of one home library started
in their last 728 seconds, and those of 95% of its episodes in their
last 256 seconds, by the marks of two databases on 2026-11-11.

The first line of `<video>.jsonl` is the header. It names the format,
`liken.sh/titles/reading/v1`, the video file's name and its `size` in
bytes, the video's length, the second the reading starts at, the two
models with the SHA-256 of each file, the device, and the size of the
decoded frames. `read` leaves a reading alone when its format, its
`size`, and its models match, and writes it again when one differs.

Each line after the header is one second:

    {"t":8432.0,"coverage":0.0031,"boxes":[[0.5,0.4722,0.0612,0.0385]],"bright":0.0113,"dark":0.9961,"change":0.0004,"lines":[["Cast",0.998,0.4722,0.0385]]}

- `boxes` holds each line of text as its center, its width, and its
  height, in shares of the frame. `coverage` is the share of the frame
  inside a box.
- `bright` is the frame's mean brightness, `dark` the share of its
  pixels that are nearly black, and `change` its mean difference from
  the frame a second before, each from 0 to 1.
- `lines` is there on the seconds the reader read: each line's text, the
  reader's confidence, and the line's center and height in shares of
  the frame's height. The reader reads a frame that holds text when it
  read no frame in the 8 seconds before, and at most the 40 largest
  lines of it.

`--records DIR` writes the readings under `DIR` in place of the
library, with each video's own directory repeated under it, for a
library that only reads. `train` and `place` take the same flag.

## The model

For each second of a reading, the model gives the probability that the
end titles have started. It reads 146 numbers: sixteen signals of the
second, and the mean of each signal over the 10, 30, and 90 seconds
before and after the second, over everything before it, and from it to
the end of the file. The signals are whether the frame holds text, how
much, how many lines, how centered, how high, and how spread they are,
how dark and how bright the frame is and how much it changed, and from
the reader the count of lines, of the names of the video's people, and
of the words that name a job, such as "directed by" and "gaffer". The
names come from the `.liken/credits.yaml` of the video's folder or of
the folder above it.

The model is 200 small decision trees, fitted by gradient boosting in
[`src/trees.rs`](src/trees.rs). The start is then the one second that
best splits the reading into "not yet" before it and "started" from it
to the end ([`src/changepoint.rs`](src/changepoint.rs)), so a few
seconds that the trees get wrong do not move it. The scores of all the
seconds make a distribution, and the confidence that `place` prints is
the share of it within 5 seconds of the start.

`place` places the videos of one folder together. The episodes of a
season mostly have end titles of one length, so each video is placed
alone first, and then again with that length as a prior, when at least
three other videos of the folder were placed with a confidence of 0.8
or more. The prior has a floor, so an episode whose titles are clearly
somewhere else keeps its own start.

## Training

`train` reads a file of labels, one video a line: the video's path, the
second its end titles start, and the name of its film or series, with
tabs between them. The operator writes that file from the marks that
the two databases gave. `train` then:

1. Fits five models, each without a fifth of the films and series, and
   places each video with the model that did not see its film or
   series.
2. Leaves out the videos whose mark is more than 30 seconds from a
   placement the model was sure of. A mark is a time a person typed
   into a database, and some are wrong by minutes.
3. Fits five models again without those videos, and chooses the
   temperature that makes the confidence match how often a placement is
   within 10 seconds of its mark.
4. Fits the model on every video it did not leave out, and writes the
   model file.

The model file is JSON in the format `liken.sh/titles/model/v1`. Beside
the trees it holds `trained`, the count of the videos, the groups, and
the seconds it was fitted on, and `held_out`, the share of the
placements of step 3 within 3, 10, and 30 seconds of their marks. The
score is against the marks and not against the truth, so a model that
is right where a mark is wrong loses that point.

## The checks

`make test` runs the format check, the lints, and the unit tests. The
tests load no model and decode no video: the box finder, the text
decoder, the signals, the trees, the placement, and a whole `train` on
readings that the test writes.
