# 96, End titles from the picture

This plan, written on 2026-11-11, finds the second that the end titles
of a video start from the video's own picture: the text on screen in
its last minutes, read by a new tool, `titles`, and placed by a small
model that each cluster fits on its own library.

Status on 2026-11-11: the tool is built, in
[`titles/`](../titles/README.md), with its three commands and their
tests. The operator does not run it yet.
[What is not built](#what-is-not-built) lists the steps that remain.

## The problem

The up-next card rises at the end titles, and both watched rules count
a play as finished there. `library-operator` learns where they start
from three places today, and each has a limit.

- **The two databases.** The `marks` fact asks TheIntroDB and IntroDB.
  On a home library on 2026-11-11, 4,679 of 8,036 episode files and
  174 of 1,527 films had a mark for their end titles from one of
  them. A mark is a time that a person typed, and some are wrong by
  minutes: against 53 videos whose start a person checked frame by
  frame, the marks of 3 of 24 films and 3 of 29 episodes were more than
  30 seconds away, and the worst was 349 seconds late.
- **The estimate.** [Plan 84](completed/84-a-larger-up-next-card.md)
  infers a start from the other episodes of a season and from the quiet
  stretches of a subtitle file. It needs neighbors with marks, or a
  text subtitle.
- **The faceless end.**
  [Plan 92](completed/92-credits-from-the-faceless-end.md) takes the
  start of the final run with no face in the `appearances` record. It
  is 84 seconds late at the median, by design, and it needs the
  `appearances` fact, which decodes the whole film.

None of them looks at the titles. End titles are text, mostly on a dark
frame, that goes on to the end of the file, and the names in them are
names that `library-operator` already holds.

## What the picture holds

These measurements ran on a workstation against a home library on
2026-11-10 and 2026-11-11, with PP-OCR's text detector and reader.

**Text alone is a weak signal.** One frame was read at eight offsets
from the databases' mark of 146 films and 285 episodes. A frame of the
story held text about a quarter of the time: a sign, a letter, a phone,
a subtitle. A rule over the share of the frame that text covers placed
the titles of 18 of 22 films within 15 seconds of the mark, and its
misses were in-story text and dark scenes.

**A known name is a strong one.** The same frames, with each line
compared with the names in the title's `.liken/credits.yaml`:

| Offset from the mark | Films: any text | Films: a known name | Episodes: any text | Episodes: a known name |
|---|---|---|---|---|
| 600 s before | 23% | 0% | 22% | 0% |
| 300 s before | 27% | 1% | 18% | 0% |
| 120 s before | 27% | 3% | 24% | 1% |
| 45 s before | 25% | 1% | 21% | 2% |
| 8 s after | 77% | 35% | 86% | 32% |
| 20 s after | 89% | 43% | 91% | 30% |
| 35 s after | 88% | 30% | 91% | 16% |
| 60 s after | 90% | 27% | 91% | 12% |

Seven of about 1,300 frames matched a name two minutes or more before
the mark, and in each the titles had started before the mark. A name
was in at least one of the four frames after the mark for 75% of the
films and 53% of the episodes. The credits fact lists a series' people
and not each episode's, and it lists the actors, the directors, and the
writers, where a credit roll is mostly crew.

**A model over the seconds beats both.** A classifier was fitted on the
seconds of 213 videos (59 films, 154 episodes), with the databases'
marks as its labels, and each video was placed by a model fitted
without its film or series. Against the checked starts:

| | Within 3 s | Within 10 s | Within 30 s |
|---|---|---|---|
| Episodes, the model (29) | 55% | 90% | 97% |
| Episodes, the databases' marks | 55% | 76% | 90% |
| Films, the model (24) | 62% | 88% | 92% |
| Films, the databases' marks | 62% | 83% | 88% |

The model's labels were the same marks it is compared with. It places
better than they do because the wrong marks are wrong in unrelated
ways and the picture is consistent: of the six videos whose mark was
more than 30 seconds off, the model placed five within 10 seconds of
the checked start. The checked set is small, so one video is 3 to 4
points.

Four more results shaped the design:

- **Trees beat a logistic regression** on the same numbers: 88% of
  episodes within 10 seconds against 78%.
- **The words matter less than the picture.** With no text read at
  all, only the boxes, their layout, and the dark share, the model lost
  three films and one episode at 30 seconds. Without the names alone it
  lost almost nothing: the words that name a job, such as "directed
  by", carry most of what the reader adds.
- **The title's own mark adds little as an input.** A placement that
  also took the mark as a prior was no closer to the checked starts.
- **A mark matched by runtime is right more often.** TheIntroDB takes
  the file's duration with the ID, and IntroDB takes the ID alone.
  TheIntroDB's marks were within 10 seconds of the model's placement
  for 90% of 21 films and 84% of 44 episodes, and IntroDB's for 66% of
  38 films and 70% of 110 episodes.

## The design

### The words

The film trade calls these sequences titles: the main titles open a
film, and the end titles close it. The job is `titles`. The mark kinds
`intro` and `credits` become `main-titles` and `end-titles` everywhere,
which leaves `credits` with one meaning in the catalog, the list of a
title's people. `recap`, `preview`, and `post-credits` keep their
names. This plan builds the end titles. The same job can read the start
of a file for the main titles later, and nothing in the reading or the
model's inputs is tied to the end.

### One job with its own decode

`titles` is a heavy fact of its own, an Indexed `Job` like `trickplay`
and `appearances`, with its own `parallelism`. It does not share a
decode with `appearances`, for two reasons that were measured:

- The decode is the smaller part of `appearances` on a node with a GPU.
  For 30 minutes of 1080p video, VA-API decode took 26 to 36 CPU
  seconds of the 74 to 77 that the whole `detect` took.
- `titles` needs only the end of the file. It seeks to the last 15
  minutes of a film or the last 6 of an episode, so its decode is a
  tenth of a whole film's.

A fact of its own also keeps a change to the text model from decoding
the faces again.

### Three steps

- **`read`** opens the video. It decodes the window at one frame a
  second, finds the lines of text in each frame, reads the text of one
  frame in 8 seconds, and writes a reading beside the video. It runs
  once for each file.
- **`train`** fits the model on the readings of the videos that a
  database has a mark for. It opens no video.
- **`place`** gives the start and a confidence from a reading and the
  model. It runs again over every reading when the model changes.

[The tool's README](../titles/README.md) holds the reading's format,
the model's inputs, and the training procedure.

### Each cluster fits its own model

The repository holds no model. A model fitted on one library would
carry that library's databases' marks into every other cluster, and a
list of the videos it was fitted on is a list of what a home holds. So
the operator fits the model in the cluster:

1. The `titles` workers write a reading for each video.
2. When enough videos have both a reading and a database's mark, a
   `Job` runs `train` on them. The operator starts it on an event, the
   count of such videos passing a threshold or growing by a share, and
   never on a timer.
3. The new model replaces the old one only when its held-out score is
   at least as good. The score and the counts go into the Library's
   status.
4. `place` runs over every reading with the new model.

Until a library has a model, it uses the databases' marks as it does
today. A small library may never have one.

### Where a placement goes

A placement is a mark of the kind `end-titles` with the source
`titles`. The estimate of
[plan 84](completed/84-a-larger-up-next-card.md) takes it as an exact
clue when its confidence is high, and the confidence that counts as
high is one of the [open questions](#open-questions). It replaces the
`faces` tier of
[plan 92](completed/92-credits-from-the-faceless-end.md), which
becomes the fallback for a video with an `appearances` record and no
reading.

### The names of the whole crew

The `credits` fact kept the billed cast, the directors, and the writers
of what TMDb returns, and dropped the rest of the crew. A designed
title sequence shows one or two people a card, and most are the people
that were dropped: the producers, the composer, the cinematographer,
the editor, the production designer.

So the `credits` fact now also writes `.liken/billing.yaml`, with every
person TMDb credits and each person's department and job. The catalog
does not read it, so the tables and the screens stay as they are, and
`titles` reads its names with those of `credits.yaml`. The file keeps
the department and the job, so `titles` can later count some
departments and not others with no new ask of TMDb.

`titles` also looks for a name in the letters of a whole frame, with no
spaces, and with the name's words in either order. A card often sets
the first name over the last in two boxes, and a match line by line
found neither "CHRIS" nor "ROCK".

The measurement used the key crew of IMDb's `title.principals` in
place of TMDb's crew, which gave a film 55 names at the median in place
of 48, and a series 159 in place of 83. Over the frames that `read`
read of 436 videos, with the two changes together:

| Frames that hold a known name | Before | After |
|---|---|---|
| Films, in the end titles (8,251 frames) | 14.4% | 19.9% |
| Films, in the story (4,796 frames) | 1.4% | 2.4% |
| Episodes, in the end titles (2,126 frames) | 12.3% | 22.4% |
| Episodes, in the story (4,452 frames) | 1.0% | 1.4% |

A frame counted as story when it was more than 30 seconds before the
video's mark, so the story rows include the titles of every video
whose mark is late.

The effect on the placements is smaller than the checked videos can
show. With and without the two changes, the placements of 16 of the
436 videos differed by more than 10 seconds. A person looked at the
frames of all 16: the changes placed 9 closer to the start, 6 farther
from it, and 1 was wrong both ways. Of the films, 7 were closer and 1
farther, and of the episodes 2 and 5. The films include three with a
designed sequence that the model had placed 100 seconds or more late.
Nine against six is no proof, and the split by kind is from too few
videos to trust.

### What it does not read

- **Faces.** The `appearances` record is not an input, so `titles`
  does not need that fact.
- **Subtitles.** The quiet stretches of
  [plan 84](completed/84-a-larger-up-next-card.md) stay in the
  estimate, and the model does not read them.
- **Audio.** [Plan 90](90-intro-marks-from-the-audio.md) asks whether
  the end of an episode should be fingerprinted for its credits. This
  plan answers that for the end titles: the picture finds them. Plan 90
  stays open for the main titles, where a title sequence shows few of
  the names the catalog holds.

[Plan 91](91-text-subtitles-from-image-subtitles.md) reads image
subtitles with Tesseract. The reader here is PP-OCR on OpenVINO, and a
build of plan 91 can weigh it against Tesseract.

## The tool, measured

The tool ran on a workstation on 2026-11-11, through
[`local/titles`](../local/titles), against the videos of a home library
and the marks of a copy of its catalog. It read 436 videos that a
database has a mark for: all 173 films with one whose file is present,
and 263 episodes of 102 series. Each got a `billing.yaml` from the key
crew of IMDb's `title.principals`, in place of the crew that the
`credits` fact now keeps from TMDb.

**The read matches the experiment.** The experiment's extractor and
`titles read` both read 107 videos. Second by second, the two agreed on
whether the frame holds text for 99.4% of the seconds at the median and
91.7% for the worst video, and the share of the frame that text covers
correlated at 0.997. An Intel GPU at its default of 16-bit floats found
text in every second of a film where the CPU found it in 42%. At 32
bits it wrote the same reading as the CPU, in the same time as two CPU
threads. So `read` runs its networks on the CPU, and a GPU is for the
decode.

**The placements match it too.** For the 53 videos with a checked start
from the experiment, placed three ways:

| | Within 3 s | Within 10 s | Within 30 s | Worst |
|---|---|---|---|---|
| The experiment: scikit-learn on its own records, 213 videos | 60% | 91% | 100% | 30 s |
| `titles train` on the experiment's records, 213 videos | 58% | 89% | 94% | 103 s |
| `titles read`, `train`, and `place`, 436 videos | 75% | 92% | 98% | 79 s |
| The databases' marks | 58% | 81% | 89% | 349 s |

The tool placed 96% of the 53 within 10 seconds of where the experiment
placed them. It has one video over 30 seconds where the experiment had
none.

**The cost of a read.** With VA-API decode and two inference threads,
the last 15 minutes of a 1080p film took 44 seconds and 540 MB, and one
thread took 79 seconds. The workstation read the videos over a wireless
link at 9 to 12 MB a second, which set the pace of the run, not the
tool.

**The cost of the model.** `train` on the 436 readings took 4 minutes
12 seconds on six threads and 108 MB. The model file is 908 KB. Two
trainings on the same readings give the same model, on four threads and
on six.

**Against checked starts.** Each video was placed by a model fitted
without its film or series. A person checked the start of 76 of the
videos frame by frame: the 53 of the experiment, and 23 picked at
random from the rest.

| | Within 3 s | Within 10 s | Within 30 s | Worst |
|---|---|---|---|---|
| The model | 71% | 89% | 97% | 79 s |
| The model, with the video's mark as a prior | 68% | 88% | 96% | 119 s |
| The databases' marks | 61% | 82% | 91% | 349 s |

By kind, the model placed 91% of the 35 films and 88% of the 41
episodes within 10 seconds, and every film within 30. The marks placed
83% and 80%. One video is 1.3 points of 76, so the first two rows do
not differ.

The 23 videos picked at random are the fairer sample, because the 53
were chosen before any model existed, and half of their starts came
first from a language model's labels. There the model and the marks
were level: 83% within 10 seconds and 96% within 30 for both, and 61%
against 65% within 3. So on a video whose mark is right, the model is
as close as the mark, and its gain is the videos whose mark is wrong.

**Where the model and a mark disagree.** An earlier model, fitted on
144 videos, placed 22 videos more than 30 seconds from their mark. A
person looked at the frames of ten of them. The model was right in
eight: five marks were in the middle of the story, two were 48 and 130
seconds into the titles, and one was after a scene that follows the
titles. The mark was right in one, a film with drawn titles that the
model placed 52 seconds late. One could not be called, an episode with
a feature about its making between its last scene and its credits.

**What the model still misses.** Two of the 76 are more than 30 seconds
late, both episodes with designed titles over a drawn or a moving
picture, and `place` gave them a confidence of 0.33 and 0.20. Of the 436
videos, the 39 with a confidence under 0.5 were within 10 seconds of
their mark for 36%, and the 397 others for 74%.

**The model is sensitive on a few videos.** A change to its inputs
moves most placements by nothing and a few by a minute:
[the names of the whole crew](#the-names-of-the-whole-crew) moved 16
of 436. The 76 checked videos hold several of those few, so a
difference of two or three videos between two fits of this plan is not
evidence for either.

**Four corrections to the fit** came from the checked starts:

- The fit left out the 5 seconds each side of every mark, because a
  mark is seldom exact. Without those seconds the model was a median 3
  seconds late, and it placed 46% of the checked videos within 3
  seconds. With them it placed 63%.
- `train` measures the lag that remains, the median of placement less
  mark, and `place` takes it from each start. On this library it is
  0.0 seconds.
- The names of the whole crew, and a name looked for across the lines
  of a frame.
- The model is the mean of four forests, each fitted on its own 70% of
  the videos, so that one video in the fit moves it less. On the
  checked videos it placed 71%, 89%, and 97% within 3, 10, and 30
  seconds, where one forest placed 63%, 91%, and 96%.

## What is built

The `credits` fact writes `.liken/billing.yaml`, and
[`local/titles`](../local/titles) runs the tool on a workstation
against a copy of a catalog. The rest is the crate
`library-operator/titles`, with a CI job, `rust-titles`:

- `titles read`, `titles train`, and `titles place`.
- The text detector's box finder and the reader's text decoder, which
  PP-OCR ships in C++ and Python that no runtime here can call.
- The gradient-boosted trees, their trainer, and the placement.
- [`titles/models/pin.toml`](../titles/models/pin.toml), with the URL,
  the SHA-256, and the license of the two models and the reader's
  character list.

## What is not built

1. **The image.** A `library-operator-titles` image with the tool,
   OpenVINO's CPU plugin, `ffmpeg`, and the models as an OCI artifact,
   as `Dockerfile.appearances` builds its image.
2. **The worker.** A `titlesWorker` in `factworkers.go`, its gap query,
   its `spec.titles` block with `parallelism` and a GPU claim template
   for the decode, and its ledger.
3. **The training `Job`,** its trigger, the guard, and the status
   fields.
4. **The rows.** The walk that copies a placement into the `marks`
   table, and the estimate's use of it.
5. **The rename** of `intro` and `credits` to `main-titles` and
   `end-titles` in `library-operator`, `media-operator`, the media
   browser, the stored ledgers, and the manuals.
6. **The manual.** The enrichment guide's section for the fact.
7. **Billing from the other sources.** The IMDb datasets and TVmaze
   still pass only the cast, the directors, and the writers. A title
   that TMDb credited before this plan has no `billing.yaml` until the
   `credits` fact asks again, which a `spec.refresh` of `credits`
   starts.

## Open questions

- **The databases' terms.** The model is fitted on marks from
  TheIntroDB and IntroDB. Nobody has read whether their terms allow
  that use. Each cluster fits its own model from the marks it already
  fetches, so the project publishes nothing derived from them, but the
  question stands for the operator of a cluster.
- **The confidence that counts.** The placement's confidence separates
  good placements from bad ones. The threshold over which a placement
  outranks a database's mark is not chosen.
- **A scene after the titles.** The placement assumes that the end
  titles run to the end of the file. A film with a scene after them
  breaks that, and how often it moves the start is not measured.
- **The main titles.** Nothing here was measured on the start of a
  file.
- **A model for a small library.** A library with too few marked
  videos never fits a model. Whether a release should carry one is not
  decided.
