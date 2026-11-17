# 96, End titles from the picture

This plan, written on 2026-11-11, finds the second that the end titles
of a video start from the video's own picture: the text on screen in
its last minutes, read by a new tool, `titles`, and placed by a small
model that each cluster fits on its own library.

Status on 2026-11-14: the tool is built, in
[`titles/`](../../titles/README.md), with its three commands and their
tests, and a `Library` that sets `spec.titles.enabled` runs a worker
that reads each video and a `Job` that fits the Library's model.

Status on 2026-11-17: a placing `Job` places the end titles of every
reading with the Library's model, and the estimate takes a placement
that the model was sure of over every other clue, as
[Placing the end titles](#placing-the-end-titles) gives it.
[What is not built](#what-is-not-built) lists the steps that remain.

Closed on 2026-11-17. The reading, the fit, the placing, and the one
answer that every screen uses are built, and so is what the placing
left behind: the operator clears a deleted Library's results of the fit
and the placing from the bus, and the close container of a library
`Job` deletes the placements of a `Library` that turns `spec.titles`
off. The parts that are not built continue as open problems:

- [A titles model with other columns waits for the library to grow](../open-problems/a-titles-model-with-other-columns-waits-for-the-library-to-grow.md)
- [An episode the model is sure of moves toward its season's usual length](../open-problems/a-sure-episode-moves-toward-its-seasons-usual-length.md)
- [Intro and credits each name two things](../open-problems/intro-and-credits-each-name-two-things.md)
- [Only TMDb gives the names of the whole crew](../open-problems/only-tmdb-gives-the-names-of-the-whole-crew.md)

The [open questions](#open-questions) at the end stand as they were
written, except the confidence that counts, which is 0.6. Plan 90 holds
the main titles.

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
- **The estimate.** [Plan 84](84-a-larger-up-next-card.md)
  infers a start from the other episodes of a season and from the quiet
  stretches of a subtitle file. It needs neighbors with marks, or a
  text subtitle.
- **The faceless end.**
  [Plan 92](92-credits-from-the-faceless-end.md) takes the
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

[The tool's README](../../titles/README.md) holds the reading's format,
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

On 2026-11-12 the design of these steps was agreed in more detail.
None of it is built.

- **What a person declares.** One block on a `Library`, `spec.titles`,
  with `enabled`, `parallelism`, and `gpuResourceClaimTemplate`, as
  `spec.appearances` has. Nothing in it names the model or the fit.
- **When the first fit starts.** When 100 videos of 30 films and series
  have a reading and a mark. A model of that size was 3 points under
  the full one within 10 seconds
  ([How many videos a model needs](#the-tool-measured)). The floor
  counts films and series because the episodes of one series show one
  style of titles, and a worker can read one long series first.
- **When a fit starts again.** When the count of videos with a reading
  and a mark has grown by half since the last fit, and when a new
  version of the tool changes the model's columns, because `place`
  refuses a model of other columns. The score stopped rising at about
  300 videos, so the later fits cost minutes and change little.
- **The numbers are constants in one place,** each with a comment that
  names its measurement, so a person who has measured another library
  can change them.
- **What rejects a model.** A floor on its held-out score, not a
  comparison with the model before it. The held-out score is against
  the marks, and it was 70% to 78% within 10 seconds for every model
  that worked, so it cannot rank two of them.
- **What a person reads.** The status holds a list with one entry for
  each kind of titles, and each entry holds the videos and the films
  and series of the fit, the marks left out, the share within 3, 10,
  and 30 seconds, and the time. A condition for the kind,
  `EndTitlesModel`, gives the state, an `Event` records each fit with
  its score, and a metric holds the score so that it has a history.
- **A placement is a fact.** It goes into a ledger beside the video,
  and the walk copies it into the catalog, as it does for the faceless
  end that the `appearances` worker records. A catalog that is built
  again from the volume then holds every placement.
- **Room for the main titles.** The first build reads the end of each
  video and fits one model. A reading's header lists the parts of the
  video that it holds, and a model file names its kind, so a reading of
  the start of a video and a model of the main titles are additions.

One question was open, whether a model belongs to a `Library` or to
every `Library` of a `Catalog`, and on 2026-11-14 it was decided for
the `Library`, as every other fact of the operator works. Every `Job` of the operator belongs to
one `Library` and mounts its one volume, and a worker's pod reads no
catalog. So a model for each `Library` can be a file at the library's
root, the fit is one more `Job` of the `Library`, and the worker that
reads a video can place it. A model for the `Catalog` needs a `Job`
that mounts every volume, and a way to give the model to those pods.
Measured on 2026-11-12: a model of the 173 films and a model of the
1,124 episodes together placed 66%, 90%, and 97% of the 120 checked
videos within 3, 10, and 30 seconds, and the one model of all of them
placed 73%, 92%, and 97%. So a model for each `Library` is level at 10
and 30 seconds and 8 videos behind at 3.

On 2026-11-15 the floor moved from 60% to 30% within 30 seconds. The
crowd-sourced marks were looser than credit starts checked by hand: one
model placed 64% of the held-out videos within 10 seconds of their
marks, and placed 92% of 88 hand-checked episodes within 10 seconds of
the real start. So a floor of 60% can reject a good model only because
a library's marks are loose. The working models scored 75% within 30
seconds on films, in the minimum fit of 100 videos, and 84% on
episodes, and a broken fit scores near zero. The floor of 30% is there
to catch a broken fit, a fault in the readings, the marks, or the
trainer, and not loose marks.

### Where a placement goes

A placement is a mark of the kind `end-titles` with the source
`titles`. The estimate of
[plan 84](84-a-larger-up-next-card.md) takes it as an exact
clue when its confidence is high, and the confidence that counts as
high is one of the [open questions](#open-questions). It replaces the
`faces` tier of
[plan 92](92-credits-from-the-faceless-end.md), which
becomes the fallback for a video with an `appearances` record and no
reading.

### Placing the end titles

This section, written on 2026-11-15, holds the design of the placement
in more detail. It replaces two earlier decisions of this plan: that a
placement is a ledger on the volume, and that the worker places the
video it reads. The reasons are in the points below.

**The catalog holds one answer, and every reader uses it.** The media
and library design computes each answer once, when the operator scans
or enriches a library, and stores it. A reader uses the stored answer
as it is, and has a fallback only for an answer that is missing. It
does not rank or merge candidates itself, so the screens stay simple
and every screen gives the same answer. Today three readers each take
every credits row of a video, group the rows that overlap, and use the
median start: the up-next card in `media-operator/display/src/marks.rs`,
the watched rule in `watched.go`, and the media browser's `watched.rs`,
which share the test table `watched.json`. With that merge, a placement
is only one more vote, and two crowd marks outvote it. So:

- The estimate (`markestimate.go`) is the one answer for the credits.
  It writes its credits row for every video that has any clue, also
  when a crowd mark wins. Today it writes no row then, so that the
  readers' median does not count the mark twice.
- The three readers use the start of the estimate's credits row. A
  video with no such row falls back to the rule of time: the last 5% of
  the video or the last 300 seconds, whichever is less. The readers no
  longer merge the candidates, and `watched.json` shrinks to match.
- `library-operator` ships the estimate change before the readers
  change. A reader on the new rule that meets an old catalog, where a
  crowd mark won and the estimate wrote no row, would fall back to the
  rule of time for that video.

**How the estimate ranks the clues of the end titles.**

1. A placement with a confidence of 0.6 or more. On the 120 videos
   checked by hand, the placements at or over 0.6 were within 10
   seconds for 98%, and the crowd marks of the same videos for 77%
   ([The confidence that counts](#open-questions)).
2. The exact clues of today: the crowd marks, a chapter whose title
   names the credits, and the start of a `preview` span.
3. The inferred clues of today: the length of the credits in the other
   episodes of the series, and the quiet stretches of the subtitles.
4. The faceless end of the `appearances` record.

Inside a tier, the latest clue wins for the end titles, because an
up-next card that rises a little late costs less than one that rises
over the end of the story. Today the earliest clue wins, and the
estimate's comment and its test change with it. The intro keeps the
earliest clue. A placement under 0.6 is not a clue. The big misses of
the models measured on 2026-11-15 had low confidence: one film placed
254 seconds late at 0.51 and one 347 seconds late at 0.21, and under
"latest wins" a late placement like those would win.

**One placing `Job` for each `Library`.** The placement is a `Job` of
its own, apart from the fit `Job` and the reading workers, and it runs
one process with several threads:

- It runs `titles place --threads N` over the readings it places, and
  names them in a file, with `--videos`, because a library holds more
  videos than a command line does. On 2026-11-17, on a workstation,
  `place` took 99 seconds for 1,297 readings on one thread, and most
  of that was the search for each known name in the text of each
  frame. Two tests that skip the search where it cannot match, and
  change no answer, took it to 34 seconds, and four threads to 5
  seconds, with the same 1,297 placements each time. A measurement of
  2026-11-15 that gave 0.4 seconds a video was taken on a slower day
  of the same workstation and is not the tool's speed.
- It gives `place` every reading of each folder it places, not one
  video alone. `place` places each video, and then places it again with
  the median length of the end titles of the other videos in its
  folder that it placed with a confidence of 0.8 or more, where there
  are three of them (`titles/src/place.rs`). An episode placed without
  its siblings can land in a different place.
- It mounts the library volume read-only. It reads the readings, the
  names in `credits.yaml` and `billing.yaml`, and the model at
  `<root>/.liken/titles/model.json`, and it writes nothing there.
- It writes each placement into the catalog: a row of the marks table
  of the kind `credits`, the source `titles`, and an ordinal below
  zero, outside the walk's write, as the estimate's rows are. The row
  carries the placement's confidence in a new column of the marks
  table, `confidence`, which is empty on every other row. The estimate
  reads it to rank the placement.
- After the rows land, it derives the Library's estimates again with
  `deriveMarkEstimates`, the step that the close container of every
  library `Job` runs.

The placing `Job` places from the picture alone. `place` can also take
a video's crowd mark as a guide, with `--marks`, and the `Job` does not
pass it. On 2026-11-17 the 120 videos checked by hand were placed both
ways, each by a model that never saw its film or series. The guide
changed how sure a placement was more than where it was: 110 of the
120 placements reached a confidence of 0.6 with it, against 82 without
it, and 5 of those 110 were more than 10 seconds from the real start,
against none of the 82. So a mark that is a little wrong makes its own
placement look sure. Under the ranking above, the answer that the
estimate keeps was within 3, 10, and 30 seconds of the real start for
93, 112, and 116 of the 120 videos without the guide, and for 86, 108,
and 115 with it.

**The intro follows the same rule.** The display's skip over the intro
merges every intro row of a video today, as the readers of the credits
do. In the same change, the estimate writes its intro row for every
video that has a clue, also when a crowd mark wins, and the display
uses that row. The spans that only the crowd marks give, `recap`,
`preview`, and `post-credits`, keep the readers' merge until a later
change gives them a stored answer.

A placement is derived from the readings and the model, and both are
on the volume. So, like an estimate, nothing on the volume holds it,
and a catalog that is built again gets its placements back from the
next placing `Job`. The worker does not place. One path writes every
placement, and a run of the placing `Job` covers a video that a
worker reads after the model exists.

**When the placing `Job` runs.** Every run places every reading of the
`Library`, and writes only the rows that changed. The first design had
a small run for the folders with a new reading, and the measurement
above made it needless: a library of 8,000 videos takes about half a
minute on four threads. The operator starts a run on an event, never on
a timer, and runs at most one for each `Library` at a time. The
reporter counts the Library's readings and its placement rows in the
catalog, and the last run's result, in the Library's status, holds the
time of the model it placed with and both counts. A run starts when:

- the model on the volume is not the model of the last run, which is
  how a new fit places the whole `Library` again;
- the catalog holds fewer placement rows than the last run wrote, which
  is how a catalog that is built again gets its placements back;
- 50 readings have landed since the last run, so that a reading worker
  that reads for days does not start a `Job` for each video;
- or any reading has landed since the last run and no reading worker of
  the `Library` runs, so that the last videos of a backfill and a
  single new episode do not wait for 50.

A video whose reading holds no end titles gets no row, because a mark
with no start means "from the start of the file" to every reader. The
run counts those videos in its result.

**What a person reads.** The entry of `status.titlesModels` for the end
titles holds the last run: the time of the model it placed with, the
readings it placed, the placements it wrote, and how many of them have
a confidence of 0.6 or more. The run posts one `Event` on the
`Library`.

**What it reuses.** The placing `Job` takes the parts of the fit `Job`
in `titlesfitjob.go` and `titlesfitrun.go`: the builder, the trigger
that reads the counts and the last run, the bus result, the deletion of
a finished `Job`, the titles image, and the pod program in it. It takes
the catalog agent of a library `Job` (`libraryJobAgent` in `pod.go`)
and `deriveMarkEstimates`. The shared parts move into one place that
both `Job`s call, and no copy of a builder or a trigger is made.

**What the build of the estimate and the readers settled,** on
2026-11-17:

- **The providers' candidates merge in the estimate.** The readers
  grouped the candidates that overlap and took the median of each
  group. The estimate now does that, so the providers' answer is one
  clue, and "the latest clue wins" compares it with a chapter and not
  with each submission.
- **A `preview` span is a limit, not a clue.** A preview follows the
  credits. Under "the earliest clue wins" its start was one more clue.
  Under "the latest clue wins" it would move the answer past the
  credits, so the answer starts no later than the preview, and a video
  with only a preview takes its start.
- **The scene in a gap becomes a row.** One provider states a scene
  after the credits as two credits spans with the scene between them,
  and the display read the second span to hold the up-next card until
  after the scene. The answer is one credits span, so the estimate
  writes the gap as a `post-credits` row, the kind that the display
  already reads a scene from.
- **The placement has a kind of its own,** `credits-placement`, so a
  screen that is given every row of the table ignores it.
- **The readers changed in two queries.** The media browser reads the
  marks for a `Play` and for its own watched rule from the catalog.
  Both queries now take the rows below zero for the credits and the
  intro, and the `Play` carries one credits span and one intro span.
  The display's code did not change: its merge reads one candidate for
  those two kinds. Both watched rules lost their merge and take the
  earliest credits span of the second half, which also reads a `Play`
  that another client wrote with several spans.
- **A catalog from before this build** has no answer for a video whose
  winner was a provider's mark, so a screen falls back to the rule of
  time for that video until the close of the Library's next `Job`.

**The first placing on a home cluster,** on 2026-11-17, with a
development build. The placing `Job` of a library of films placed
1,530 readings in 61 seconds, and that of a library of series placed
8,065 in 2 minutes 11 seconds, with the catalog's sync and hand-off.
The model of the films was sure of 242 of its 1,494 placements, and the
model of the series of 7,357 of its 8,032. Against the starts that a
person checked:

| | Checked | Within 3 s | Within 10 s | Within 30 s |
|---|---|---|---|---|
| The answers for films | 35 | 22 | 28 | 31 |
| Of them, from a sure placement | 13 | 12 | 12 | 13 |
| The answers for episodes | 88 | 61 | 82 | 85 |
| Of them, from a sure placement | 81 | 61 | 79 | 79 |

The answers of the films that are far off, 258 and 349 seconds late,
are the one crowd mark of each film, where the model was not sure.

One difference from the earlier checks needs a decision. Those checks
placed only the checked episodes. The `Job` places a whole library, so
`place` also places each episode again with the usual length of the end
titles of its folder, and it does that for an episode it was already
sure of. For 20 of the 88 episodes that moved the start by 2 to 5
seconds toward the folder's usual length and raised the confidence from
about 0.8 to about 0.95. Placed alone, 69 of the 88 were within 3
seconds and 83 within 10. Placed with their folders, 62 and 82. Whether
an episode that `place` is sure of alone should keep its own start is
not decided.

**The steps, in the order they ship.**

1. Profile `place`, and give it `--threads`. Built on 2026-11-17.
2. Add the `confidence` column to the marks table. Built on
   2026-11-17.
3. Build the placing `Job`, its trigger, and its result in the status.
   Built on 2026-11-17.
4. Rank the placement in the estimate, change the end titles to "latest
   wins", and write the credits row for every video with a clue. Built
   on 2026-11-17.
5. Change the readers and `watched.json` to the estimate's row and the
   rule of time. Built on 2026-11-17.
6. Update the enrichment guide, the `Library` reference, and the skills
   for the placing `Job`, its status, and its `Event`s. Built on
   2026-11-17.

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
  [plan 84](84-a-larger-up-next-card.md) stay in the
  estimate, and the model does not read them.
- **Audio.** [Plan 90](../90-intro-marks-from-the-audio.md) asks whether
  the end of an episode should be fingerprinted for its credits. This
  plan answers that for the end titles: the picture finds them. Plan 90
  stays open for the main titles, where a title sequence shows few of
  the names the catalog holds.

[Plan 91](../91-text-subtitles-from-image-subtitles.md) reads image
subtitles with Tesseract. The reader here is PP-OCR on OpenVINO, and a
build of plan 91 can weigh it against Tesseract.

## The tool, measured

The tool ran on a workstation on 2026-11-11, through
[`local/titles`](../../local/titles), against the videos of a home library
and the marks of a copy of its catalog. Its first run read 436 videos
that a database has a mark for: all 173 films with one whose file is present,
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

**The full run.** The workstation then read more marked episodes, and
`train` ran again on 1,297 readings: the 173 films, and 1,124 episodes
of 134 series. It took 6 minutes 30
seconds on twelve threads and 188 MB, and the model file is 908 KB. It
left out 144 marks as wrong, 11% of them. The figures from here on are
this run's.

**Against checked starts.** Each video was placed by a model fitted
without its film or series. A person checked the start of 120 of the
videos frame by frame: the 53 of the experiment, and 67 picked at
random from the rest.

| | Within 3 s | Within 10 s | Within 30 s | Worst |
|---|---|---|---|---|
| The model | 73% | 92% | 97% | 85 s |
| The model, with the video's mark as a prior | 72% | 93% | 96% | 85 s |
| The databases' marks | 58% | 77% | 87% | 349 s |

By kind, the model placed 94% of the 35 films and 91% of the 85
episodes within 10 seconds, and every film within 30. The marks placed
83% and 74%. One video is 0.8 points of 120, so the first two rows do
not differ.

The 67 videos picked at random are the fairer sample, because the 53
were chosen before any model existed, and half of their starts came
first from a language model's labels. There the model placed 75%, 93%,
and 97% within 3, 10, and 30 seconds, and the marks 58%, 73%, and 85%.
The first 23 of the 67 came from the films and the 263 episodes of the
first run, and there the model and the marks were level at 83% within
10 seconds. The other 44 are episodes, and the marks of episodes are
wrong more often.

**More videos did not change the score.** On the 96 videos that were
checked before the full run, the model of 436 videos placed 71%, 91%,
and 98% within 3, 10, and 30 seconds, and the model of 1,297 placed
70%, 91%, and 97%.

**How many videos a model needs.** The 96 checked videos were split in
three by their film or series. For each third, `train` ran on a share
of the other films and series, and the model placed the third. So each
row scores the same 96 videos, each by a model that has none of its
film or series. The count of videos and of films and series is the
mean of the three fits.

| Videos | Films and series | Within 3 s | Within 10 s | Within 30 s | Worst |
|---|---|---|---|---|---|
| 48 | 16 | 69% | 84% | 96% | 122 s |
| 107 | 32 | 68% | 89% | 98% | 69 s |
| 293 | 64 | 60% | 92% | 98% | 67 s |
| 519 | 129 | 65% | 91% | 95% | 118 s |
| 728 | 193 | 62% | 91% | 98% | 65 s |
| 926 | 257 | 69% | 92% | 98% | 67 s |

The score within 10 seconds stops rising at about 300 videos of 64
films and series, and a model of 107 videos of 32 is 3 points under
it. The films gain the most from more videos: 74% of the 35 within 10
seconds at the first row, 86% to 89% in the middle rows, and 94% at
the last. The episodes are at 90% to 93% in every row. The trainer's
own held-out score, which is against the marks, was 70% to 78% within
10 seconds in every row and shows no trend, because a share of the
marks are wrong. So that score can show that a model is broken, but it
cannot rank two models that work.

**Where the model and a mark disagree.** An earlier model, fitted on
144 videos, placed 22 videos more than 30 seconds from their mark. A
person looked at the frames of ten of them. The model was right in
eight: five marks were in the middle of the story, two were 48 and 130
seconds into the titles, and one was after a scene that follows the
titles. The mark was right in one, a film with drawn titles that the
model placed 52 seconds late. One could not be called, an episode with
a feature about its making between its last scene and its credits.

**What the model still misses.** Four of the 120 are more than 30
seconds late, all episodes, and three of them have designed titles
over a drawn or a moving picture. `place` gave the four a confidence
of 0.24, 0.33, 0.38, and 0.53.

**The confidence separates them.** Of the 120 checked videos, 105 have
a confidence of 0.6 or more. The model placed 98% of those within 10
seconds and all of them within 30, where the marks of the same 105
placed 77% and 88%. It placed 5 of the 8 from 0.5 to 0.6 within 10
seconds, and 2 of the 7 under 0.5. Against the marks of all 1,292
placed videos, the 35 under 0.5 were within 10 seconds for 29%, and
the others for 73%.

**The model is sensitive on a few videos.** A change to its inputs
moves most placements by nothing and a few by a minute:
[the names of the whole crew](#the-names-of-the-whole-crew) moved 16
of 436. The checked videos hold several of those few, so a difference
of two or three videos between two fits of this plan is not evidence
for either.

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
  76 videos checked at the time it placed 71%, 89%, and 97% within 3,
  10, and 30 seconds, where one forest placed 63%, 91%, and 96%.

## What is built

The `credits` fact writes `.liken/billing.yaml`, and
[`local/titles`](../../local/titles) runs the tool on a workstation
against a copy of a catalog. The rest is the crate
`library-operator/titles`, with a CI job, `rust-titles`:

- `titles read`, `titles train`, and `titles place`. A reading lists
  the parts of the video that it holds, and a model names its kind.
- The text detector's box finder and the reader's text decoder, which
  PP-OCR ships in C++ and Python that no runtime here can call.
- The gradient-boosted trees, their trainer, and the placement.
- [`titles/models/pin.toml`](../../titles/models/pin.toml), with the URL,
  the SHA-256, and the license of the two models and the reader's
  character list.

In the operator:

- **The worker.** `titlesWorker` in `factworkers.go`, its gap of every
  main video with a length and no reading, the `spec.titles` block with
  `enabled`, `parallelism`, and a GPU claim template for the decode,
  and the attempt in `.liken/titles.yaml`.
- **The image.** `library-operator-titles`, from `Dockerfile.titles`,
  with the tool, OpenVINO's CPU plugin, the pod program, and the models
  as an OCI artifact that `titles/models/push.sh` pushes.
- **The fit.** The reporter counts the videos with a reading and a
  provider's mark (`titlesfit.go`), the operator starts a fit `Job` at
  the counts above (`titlesfitjob.go`), and the `Job` reads the readings
  and the marks ledgers from the volume, runs `train`, and lands
  `.liken/titles/model.json` under the root unless the model is under
  the floor (`titlesfitrun.go`). The close container of every library
  `Job` removes the other files of the root's `.liken/` and keeps
  `.liken/titles/` (`rootliken.go`). Its result goes over the bus into
  `status.titlesModels`, the `EndTitlesModel` condition, and two gauges.
  On 2026-11-14 the `Job`'s program ran from the image on the readings
  of the 173 films, with a broker: it fitted in 3 minutes 15 seconds on
  four threads, placed 77% of the held-out films within 30 seconds of
  their marks, and published the result.
- **The manual.** The enrichment guide's section for the fact.

## What is not built

On 2026-11-17 this plan closed, and each part below continues as an
open problem that the list at the top of the plan links to.

1. **A fit when the tool's columns change.** A release of the tool
   with other columns refuses the model a Library has, and nothing
   starts a fit for that until the Library's count has grown by half.
2. **The rename** of `intro` and `credits` to `main-titles` and
   `end-titles` in `library-operator`, `media-operator`, the media
   browser, the stored ledgers, and the manuals.
3. **Billing from the other sources.** The IMDb datasets and TVmaze
   still pass only the cast, the directors, and the writers. A title
   that TMDb credited before this plan has no `billing.yaml` until the
   `credits` fact asks again, which a `spec.refresh` of `credits`
   starts.
4. **The start of an episode the model is sure of.** Placed with its
   folder, such an episode moves a few seconds toward the folder's
   usual length, as
   [Placing the end titles](#placing-the-end-titles) measured.

## Open questions

- **The databases' terms.** The model is fitted on marks from
  TheIntroDB and IntroDB. Nobody has read whether their terms allow
  that use. Each cluster fits its own model from the marks it already
  fetches, so the project publishes nothing derived from them, but the
  question stands for the operator of a cluster.
- **The confidence that counts.** The placement's confidence separates
  good placements from bad ones. The threshold over which a placement
  outranks a database's mark is not chosen. On the 120 checked videos,
  the placements of 0.6 or more were within 10 seconds for 98%, and
  the marks of the same videos for 77%.
- **A scene after the titles.** The placement assumes that the end
  titles run to the end of the file. A film with a scene after them
  breaks that, and how often it moves the start is not measured.
- **The main titles.** Nothing here was measured on the start of a
  file.
- **A model for a small library.** A library with too few marked
  videos never fits a model. Whether a release should carry one is not
  decided. Neither is the count that is enough. On this library a
  model of about 100 videos of 32 films and series was 3 points under
  the full one, but nobody has measured the count on another library.
