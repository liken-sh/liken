# 08, The component and the first session

Proposed and designed on 2026-10-12. Pieces B and D of stage 1 were
built on 2026-10-12, and [what they built](#what-b-and-d-built) lists
them with each deviation. Pieces E, F, and G of stage 2 were built
on 2026-10-12, and [what E built](#what-e-built),
[what F built](#what-f-built), and [what G built](#what-g-built) list
them the same way. Pieces H and I of stage 3 were built on
2026-10-12, and [what H built](#what-h-built) and
[what I built](#what-i-built) list them the same way. [The early
run](#the-early-run) records a session of E, F, and G on the dev
cluster, and the fault it fixed. [The first session](#the-first-session)
records the drill of every piece together on 2026-10-12, the fault it
fixed, and the faults it found. This plan builds
first: it gives the component that plans 01, 02, and 03 build into,
the changes they need from `observatory-operator`, the order of the
build, and the drill that proves them together.

## The problem

`astrophotography-operator` holds only plans. Plans 01, 02, and 03
are designed, and together they make one imaging session: a person
declares a `Target` and a `Reservation` held by this operator, and the
operator slews, solves and centers, guides, exposes the sequences, and
moves each labeled frame to the target's archive. Several agents will
build the pieces at once, so the component's shape, the contracts
between the pieces, and their order are decided before the first line
of code.

## Importing `observatory-operator`'s packages

This operator imports three packages of `observatory-operator`, the
first import between two operators of the repository:

- **`indi`**, the INDI client. It holds what the drills of
  `observatory-operator` learned about `indiserver` and libindi: the
  order of `getProperties` and `enableBLOB`, how `Settle` reads an
  answer, and the escape of `'` and `"` that commit 5e6ce6e0 fixed
  after a drill. This operator drives the same server with the same
  protocol, and a second client would have to learn each of those
  again.
- **`phd2`**, the client of PHD2's event server, and `phd2/phd2test`,
  its fake, in tests.
- **`observatory`**, the Go types of the resources this operator reads,
  which `observatory-operator`'s tests hold equal to its CRDs, the list
  of keywords the driver writes (`RefusedFITSKeywords`), and
  `MergeFITSKeywords`.

The three packages import only the standard library and
`kubernetes/conditions`, so the import links no more of
`observatory-operator` than they are. This operator's `go.mod` requires
`github.com/liken-sh/liken/observatory-operator` at `v0.0.0`, with a
`replace` to `../observatory-operator`, the way every operator takes
`kubernetes/`. The `Dockerfile` takes `observatory-operator/` as a
build context named `observatory`, and copies its module files and the
three packages. `package.toml` lists `observatory-operator` among the
components it depends on, so a change to those packages runs this
operator's tests too. The dependency goes the direction that root plan
74 already gives the two components.

Root plan 65 recorded that the operators import nothing from each
other, as a fact of that day and a reason not to make a shared module
of fifty lines. The reason does not apply here: the shared code is a
protocol client with its tests, thousands of lines, and it already has
a home that names its domain.

These were set aside:

- **Hand-written copies of the types,** the way `library-operator`
  copies the few fields of `media-operator`'s `Player` that it reads.
  That works for a few fields of a resource. The INDI and PHD2 clients
  are protocol code, and two copies would drift, as the eight copies of
  the watch loop did before the operators moved to client-go.
- **A shared module for INDI** at the top of the repository. A module
  named for no domain is a junk drawer, and `indi/` at the top is the
  image component, where a new file changes the recipe of the 17 INDI
  images.

## Changes to `observatory-operator`

Each is small, and each is a commit of that component with its own
tests and manual:

1. **PHD2's guiding methods.** `phd2.Client` gains `SetExposure`,
   `Loop`, `FindStar`, `Guide` with a settle and `recalibrate`, and
   `Dither` with an amount, `raOnly`, and a settle. `State` gains the
   settle: `SettleBegin`, `Settling`, and `SettleDone` with its status
   and error. `phd2test` answers each method and sends the events.
2. **A fake INDI server for other components' tests.** The replay of
   the transcripts in `indi/testdata` moves into an exported package,
   `indi/inditest`, on `net.Pipe`, so this operator's tests run the
   real client against the simulators' real messages. It stays on the
   standard library.
3. **`spec.holder` as a selectable field** of the `Reservation`'s CRD,
   so this operator lists and watches only the reservations it holds.
4. **`FRAMEID` refused** in `spec.fitsKeywords` of every level, with the
   message that this operator writes it, so a frame of a desktop
   session never carries a declared identity.
5. **`AGENTS.md`** says that `indi`, `phd2`, and `observatory` have a
   second user, so a change to them runs both components' tests.

Piece C was built on 2026-10-12, one commit for each change, except
the fifth, which is in the commit of the second:

- `phd2.Client` has `SetExposure(ctx, time.Duration)`, `Loop(ctx)`,
  `FindStar(ctx) (x, y float64, err error)`, `Guide(ctx, Settle,
  recalibrate bool)`, `Dither(ctx, amount float64, raOnly bool,
  Settle)`, and `WaitSettled(ctx)`, which answers `nil`, an error that
  wraps `ErrSettleFailed` with PHD2's reason, `ErrDisconnected`, or
  `ErrNotSettling`. `Settle` is `{Pixels, Time, Timeout}`, and PHD2
  reads the times as whole seconds, so the request rounds them up.
  `State.Settling` records the settle. The replay of the recorded
  session drives `loop`, `find_star`, and `guide` and checks their
  params. No recording holds `set_exposure` or `dither`, so only the
  fake, which follows `event_server.cpp` and `phdcontrol.cpp`, holds
  them; the drill's step 9 is their first check against PHD2.
- `indi/inditest` holds the transcripts, embedded, in
  `indi/inditest/testdata/`, and the driver model of
  `observatory-operator`'s fake servers, not the scripted replay of
  `indi/replay_test.go`. The replay answers only the messages that the
  recorder sent, and the runner of plan 01 writes values of its own.
  `inditest.NewServer(t, "telescope", "ccd", "wheel")` serves the
  simulators named, and `Hold`, `Refuse`, `Move`, `SetState`,
  `Report`, `Message`, `Property`, `Changes`, `Cut`, and `Close`
  script it. `Move` and then `SetState` play an exposure, and
  `Message` the driver's message that names the saved file.
- `Reservation` declares `.spec.holder` beside `.spec.telescope` in
  `selectableFields`.
- `RefusedFITSKeywords` includes `AstrophotographyFITSKeywords`,
  `FRAMEID`, so a `Target`'s rule that takes the list refuses it
  too.

## The scaffold

- **The module** is `github.com/liken-sh/liken/astrophotography-operator`,
  Go 1.27.1, client-go v0.36.3 in the lean form of the `operators`
  skill, with the `replace` directives above and the `go-test-coverage`
  tool.
- **The layout** follows `observatory-operator`: `package main` flat at
  the top, one domain to a file (the runner, the exposure, the
  keywords, the names, centering, guiding, the promotion `Job`s, the
  `Solver`, the worker, `Event`s, metrics), and these packages:
  - `astrophotography/`: the Go types of `Target` and `Solver`, with
    the drift, enum, validation, and example tests of
    `observatory/`;
  - `sky/`: the conversion between J2000 and JNow (plan 02);
  - `fits/`: a reader of a FITS header;
  - `catalog/`: the line of the catalog, its reader, and the totals
    (plan 01);
  - `promote/`, `worker/`, and `solver/`: the three modes of the binary
    that do not run the operator (plans 02 and 03).

  The binary takes its mode as its first argument: none runs the
  operator, and `promote`, `worker`, and `solver` run the others.
- **The contracts first.** The first commit fixes the shapes that the
  pieces share, as Go types with tests: the outbox's directories (plan
  01), the catalog's line (plan 01), the promotion report (plan 03),
  and the worker's and the solver's HTTP requests and answers (plan
  02). Then the pieces build against them at the same time.
- **`deploy/`**: `targets-crd.yaml` and `solvers-crd.yaml`, written by
  hand, `rbac.yaml`, `operator.yaml`, and `kustomization.yaml` with the
  namespace `liken-system` and no other namespace.
  `examples/first-session.yaml` holds the drill's `Solver`, `Target`,
  claims, and `Reservation`, and a test holds it to the CRDs.
- **The operator** is one `Deployment` in `liken-system`, one replica,
  with a `Lease` so one copy writes, as `observatory-operator` does. It
  serves its metrics under root plan 65 on port 9200: frames exposed,
  frames archived, solves and their duration, centering rounds and the
  last error, and the bytes and failures of promotion.
- **RBAC**, one `ClusterRole`:

  | Resources | Verbs |
  |---|---|
  | `observatory.liken.sh`: `reservations`, `observatories`, `telescopes`, `opticaltrains`, `opticaltubes`, `mounts`, `cameras`, `filterwheels`, `focusers`, `guiders` | `get`, `list`, `watch` |
  | `astrophotography.liken.sh`: `targets`, `solvers` | `get`, `list`, `watch` |
  | `targets/status`, `solvers/status` | `update` |
  | `deployments`, `services` | `get`, `list`, `watch`, `create`, `update`, `delete` |
  | `jobs`, `pods` | `get`, `list`, `watch`, `create`, `delete` |
  | `persistentvolumeclaims` | `get`, `list`, `watch`, `update`, `patch` (the finalizer) |
  | `events` | `create`, `patch` |

  A `Role` in `liken-system` grants its `Lease`. The operator writes no
  spec and no status of `observatory-operator`'s resources.
- **The images**, from `package.toml`:
  - `astrophotography-operator`, from `Dockerfile`: the static binary
    on `scratch`, with the notices, like `observatory-operator`'s. The
    promotion `Job`s run it too.
  - `astrophotography-operator-worker`, from `Dockerfile.worker`:
    `FROM astrometry`, plus the same binary. The solver and the worker
    run it. The operator derives its reference from its own image's,
    with the same tag, as `library-operator` derives its companions.

  The contexts are `kubernetes`, `observatory`, and `notices`, and the
  dependencies are `astrometry`, `kubernetes`, `notices`, and
  `observatory-operator`. Run `make workflows` after the file exists.
- **`package.toml`** has the `go`, `docs`, and `prek` jobs of
  `observatory-operator`, `outputs.deploy = "deploy"`, and the docs
  prefix `astrophotography`. `Makefile`, `.testcoverage.yml`, and
  `.pre-commit-config.yaml` start as copies of `observatory-operator`'s.
- **`docs/`** is a Hugo site like `observatory-operator`'s, at
  https://liken.sh/astrophotography/: a front page; concepts for
  targets, the frame catalog, solving and centering, and promotion;
  guides to install the operator, set up a solver, image a target, and
  troubleshoot; and the reference pages of `Target` and `Solver`
  generated from the CRDs, the metrics, and the catalog's line. The
  guides emit the skills in `skills/`. The manual changes with the
  code, as the root `AGENTS.md` requires.
- **The tests** run in `synctest` bubbles against
  `kubernetes/apiservertest`, `indi/inditest`, and `phd2test`. The
  worker and the solver are faked with `apiservertest.Start`. The
  `promote` mode runs against files in `t.TempDir()`. The `solver`
  mode's queue runs against a fake engine, a Go program that reads an
  `.axy` and writes the files the way the probe of plan 02 recorded,
  because CI has no `astrometry-engine`.
  The real engine runs only in the image's smoke check and the drill.

## The order of the build

| Stage | Piece | Needs |
|---|---|---|
| 1 | A. The `astrometry` image component (plan 02) | nothing |
| 1 | B. The scaffold, the types and CRDs of `Target` and `Solver`, the contracts, `deploy/`, and the manual's skeleton | nothing |
| 1 | C. The changes to `observatory-operator` above | nothing |
| 1 | D. `sky/` with Meeus's worked examples | nothing |
| 2 | E. The runner of plan 01, unguided and with no centering | B, and C for `inditest` |
| 2 | F. Promotion and the catalog of plan 03 | B |
| 2 | G. The `Solver`, the solver mode, and the worker of plan 02 | A, B |
| 3 | H. Guiding and dithering in the runner | E, C |
| 3 | I. Centering in the runner | E, G, D |
| 4 | The drill below | all |

A, B, C, and D share no file and build at the same time. E, F, and G
build at the same time against B's contracts. The build of A downloads
Debian packages from the snapshot; on a slow link, its agent measures
the download first.

## What B and D built

Pieces B and D were built on 2026-10-12, against
`observatory-operator` as it was before piece C.

- **B, the scaffold.** The module, with the `replace` of
  `observatory-operator`, and the `Makefile`, `.testcoverage.yml`, and
  `.pre-commit-config.yaml` of `observatory-operator`. `package main`
  elects its leader with the `Lease` `astrophotography-operator`,
  opens the watches, and serves layers 1 and 2 of root plan 65 on port
  9200 under the prefix `astrophotography_`. The watches are the
  `Target`s and `Solver`s, the `Reservation`s selected by
  `spec.holder=astrophotography-operator`, the nine kinds of
  `observatory-operator` in the RBAC table, and the pods, `Service`s,
  `Deployment`s, and `Job`s that carry the operator's label. No loop
  reads them yet: `operator.run` is where E, F, and G start theirs.
- **B, the types and CRDs.** `astrophotography/` and
  `deploy/targets-crd.yaml` and `deploy/solvers-crd.yaml`, with the
  drift, enum, validation, defaults, and example tests of
  `observatory/`, and a test that holds the `Target`'s
  `spec.fitsKeywords` to `observatory-operator`'s CRD.
- **B, the contracts.** `outbox.Frame` gives a frame's path, its
  `UPLOAD_SETTINGS`, and its parse. `catalog.Line` and
  `catalog.Settings` give the catalog's line. `promote.Report` gives
  the promotion report, and a test holds 25 totals inside the 4,096
  bytes of the termination message. `worker.ExtractRequest`,
  `worker.ExtractAnswer`, `worker.OutboxAnswer`, and
  `solver.SolveAnswer` give the HTTP messages, with their paths and
  port 8080. A test fixes the JSON of each.
- **B, the rest.** `deploy/` with the RBAC of the table above, the
  `Deployment`, the kustomization, the monitoring component with a
  dashboard of layers 1 and 2, and `examples/first-session.yaml`. The
  `Dockerfile`, `package.toml`, the generated workflows, and a manual
  with a front page, the concepts of `Target` and `Solver`, the
  install guide and its skill, the metrics, and the generated
  reference pages. The image built on the laptop at 28.1 MB.
- **D, `sky/`.** Precession by the rigorous method of chapter 21 and
  nutation by the abridged series of chapter 22, as rotation matrices,
  so `FromDate` is the transpose of `ToDate`. The tests hold examples
  7.a, 17.a, 21.b, 22.a, and the nutation of 23.a. Plan 02 asks for
  no altitude or azimuth, so chapters 12 and 13 are not in it. The
  package also reads the `HH:MM:SS.s` and `±DD:MM:SS.s` of a
  `Target`, and gives the separation of two positions for the
  centering error.

The deviations, each with its reason:

- **No `Dockerfile.worker` yet.** A Dockerfile names a base of the
  repository bare, and `astrometry` is piece A, so the worker image,
  its `Dockerfile.worker`, and the dependency on `astrometry` come
  with G.
- **`outbox/` is a package** that the scaffold's list does not name.
  The runner, the worker, and the promotion `Job` all read the layout
  of plan 01's outbox, and the layout is one domain.
- **`catalog/` holds only the line.** Its reader and the totals are
  F's, and `fits/` is F's and G's.
- **No series of layer 3.** Each piece of stage 2 adds its own
  series with the code that sets it, so the manual never lists a
  series that nothing sets.
- **The nutation is the abridged series**, accurate to 0.5 arcseconds
  in longitude and 0.1 in obliquity. Centering syncs with the same
  conversion on both sides, so an error of it cancels, and the slew
  before the first sync misses by more from aberration, which plan 02
  also leaves out.
- **Fields the plans left to the build.** `status.progress` holds the
  `37/180` of the printer column, because a column cannot add up a
  list. `status.centering.solution` groups the solved center, the
  rotation, and the scale, which are absent when no frame solved.
  `Complete` is `True` with the reason `Archived`, and
  `ArchiveAvailable` with `Promoted`. Each catalog line has
  `"record": "frame"`, so plan 06's grade can be a line of another
  record that an older reader skips. The solver answers
  `GET /indexes`, which the operator copies into `status.indexes`.
  The CRD refuses an `archive.path` or `indexFiles.path` that starts
  with `/` or holds `..`, and bounds `binning` at 16,
  `centering.attempts` at 20, and `dither.pixels` at 100.
- **Before C,** the API server refuses the list of `Reservation`s by
  `spec.holder`, and the reflector retries until C's CRD is applied.
  The `Target` refuses `OBJECT` and `FRAMEID` whether or not C adds
  `FRAMEID` to `observatory-operator`'s list.

## What F built

Piece F, promotion and the catalog of plan 03, was built on
2026-10-12, at the same time as E and G and against B's contracts.

- **The seam with the runner is the `Target`'s status.** Plan 08
  named no Go call for the runner to say that a frame waits, and
  plan 01's "count the frame as pending, and wake the promotion" is
  met by the status alone. After each exposure, the runner adds one to
  the sequence's `status.sequences[].pending` and to the
  `status.outboxes[]` entry of the reservation's outbox claim, in one
  status write, and creates the entry with `pending: 1` when the claim
  has none. The runner keeps the entry's other fields, never removes
  an entry, and never writes `archived`, `progress`, or
  `status.promotion`. The write wakes the promotion loop through the
  watch of the `Target`s, and the loop starts a `Job` from what the
  status says. So the runner and the loop share no memory, and a
  restart loses nothing.
- **The contract changes.** `catalog.Line` names its fields by the
  rule that plan 03 records in [the names in the
  catalog](03-promotion-to-the-archive.md#the-names-in-the-catalog):
  `sizeBytes`, `startTime` (from `DATE-OBS`, as RFC 3339 in UTC),
  `exposureSeconds`, and `binning`, in place of `size`, `dateObs`,
  `exptime`, and `xbinning`. `promote.Report` gains `left`, the frames
  the `Job` left in the outbox, because `waiting` and `failures` can be
  cut to fit the termination message. `promote.Total` is
  `catalog.Total`, which the catalog's reader counts. The `Target`'s
  status gains `outboxes[].left` and `promotion.applied`, in the Go
  types and the CRD.
- **`catalog/`** holds the reader, which skips a line that does not
  decode and a record it does not know, and counts each `FRAMEID`
  once, and `Append`, which starts a new line after a line that a
  crash cut short and syncs the file and its directory.
- **`promote/`** holds the `promote` mode of the binary
  (`astrophotography-operator promote --target ... --path ...
  --cameras ...`), the reader of a FITS header, and `promote.Name`,
  which renders `archive.format` for the runner. The mode exits 0 when
  it reached every frame, failures included, and 1 when it stopped,
  such as on a catalog that cannot be read, so the `Job`'s next pod
  runs it again.
- **The operator** runs the loop of `promotion.go`. For each target it
  applies the report of the target's next `Job` when that `Job` has
  ended, reports `Unschedulable` while that `Job`'s pod cannot start,
  or starts the next `Job` for the first outbox claim that needs one.
  `outboxfinalizer.go` keeps the finalizer, and `promotionstatus.go`
  composes the status from a report. The `Job`s of a target are
  numbered, `<Target>-promote-<n>`, and the report of `Job` n is
  applied in the same write that sets `status.promotion.applied` to n,
  so a report is applied once and two passes never create two `Job`s.
  The `Job`'s owner is its `Target`.
- **The counts.** A sequence's `archived` is the report's total, and
  its `pending` drops by as many frames as `archived` rose. The `Job`
  records the outbox's `pending` when it is created, in the annotation
  `astrophotography.liken.sh/pending`, and the report sets the outbox's
  `pending` to the frames the `Job` left plus the frames the runner
  counted since. A frame that the runner counts while the `Job` moves
  it counts once too often, and the next `Job`, which finds nothing,
  corrects it. A frame is never counted too few times.
- **A frame that fails** starts no `Job` again on its own. A `Job`
  starts when the outbox's `pending` is more than its `left`, and once
  for each outbox in each start of the operator, so a restart retries
  a failure that a person fixed. Without that rule, a frame with a
  `NameConflict` would start a `Job` on every pass.
- **Tests.** The promote mode runs against an outbox and an archive in
  `t.TempDir()`: a crash after each step of the copy and of the rename,
  then a second run, with one file and one line in the end; the same
  claim's rename; a name that another file holds; a copy that reads
  back with another hash; a frame cut short; a catalog, an archive, or
  an outbox that refuses a write; and the tokens and the suffix of a
  name. The loop runs in a `synctest` bubble against
  `kubernetes/apiservertest`, with `fakeobjects_test.go` answering a
  read, a create, an update, a merge patch, and a delete of one object,
  and a stale `resourceVersion` with 409. The coverage is 98.0%, at the
  floor.

The deviations, each with its reason:

- **No node affinity.** The `Job` names no node, as plan 03 states:
  the scheduler places the pod by its claims, and a `local-path`
  outbox's volume carries the node.
- **A rename that falls back to a link.** An NFS or SMB share refuses
  `RENAME_NOREPLACE` with `EINVAL`, and many archives are on such a
  share, so the rename falls back to a hard link and an unlink, which
  fails with `EEXIST` the same way. A share that refuses hard links
  too fails each frame with `CopyFailed`; no drill has run on one.
- **The header's reader is in `promote/`,** not in `fits/`, because G
  builds at the same time and F's reader needs only the cards that
  promotion reads. It moves to `fits/` when a second user needs it.
- **`NoFrameID` waits.** A file with no `FRAMEID` in a target's
  directory is reported as waiting with the reason `NoFrameID`, and
  `left` does not count it, so it holds no finalizer.
- **The empty values of a name.** `{filter}` and `{train}` with no
  value render as `none`, and `{binning}` with no value as `default`,
  like `{gain}` and `{offset}`. A part of the path that starts with
  `.` starts with `_` instead, so a value of `..` cannot leave
  `archive.path` and no frame is hidden beside the `.part` files.
- **`AfterReservation`** waits while a reservation held by this
  operator that writes to the claim, by its session's outbox, is
  `Activating`, `Ready`, or `Deactivating`, and not only for the one
  reservation that wrote the frames, which the status does not name.
- **The watch of every `PersistentVolumeClaim`.** The claims carry no
  label of the operator, so the watch takes every claim of the
  cluster, for the archive's `ClaimMissing` and the outbox's
  finalizer.
- **A `Job` that succeeded waits for its pod.** The watches of `Job`s
  and pods deliver their changes apart, so the loop applies the report
  of a `Job` that succeeded only when the pod that holds it is in the
  store.
- **The dashboard** of the monitoring component does not show the
  three series of promotion yet.

Open after F:

- **The finalizer may hold frames that no `Job` can move.** The
  scheduler refuses a pod that mounts a claim being deleted, as far as
  the build read, so after a delete of an outbox claim the finalizer
  keeps the frames, and no promotion `Job` can start to move them. The
  manual tells a person to move the frames before deleting the claim.
  A drill confirms the refusal, and plan 03 decides whether the
  finalizer stays.
- **A deleted `Target`** takes its status, so the finalizer comes off
  its outbox claims while its frames are still in them.

## What E built

Piece E, the runner of plan 01 with no guiding and no centering, was
built on 2026-10-12, after F, and counts frames into F's seam.

- **The runner.** `runners.go` starts one runner for each
  `Reservation` of this operator whose phase is `Ready` with a
  session, and stops it when the phase changes. The runner keeps one
  INDI connection open, with a backoff of 1 second that doubles to a
  minute. It finishes the frames in flight of its reservation, claims
  the eligible target by plan 01's order with a write that states the
  `resourceVersion` it read, and then syncs the devices, slews, and
  exposes one frame at a time in plan 01's order of writes:
  `UPLOAD_MODE`, `UPLOAD_SETTINGS`, `CCD_FRAME_TYPE`, the binning,
  gain, and offset that the sequence states, `FILTER_SLOT`, and
  `FITS_HEADER` with `INDI_CLEAR` first and `FRAMEID` last. Each frame
  is named with `promote.Name` and counted in the one status write of
  F's seam. When the reservation leaves `Ready`, the supervisor stops
  the runner and clears `status.reservation` and `status.exposure` of
  its targets, with `Imaging` `False` and `Waiting`. A runner that the
  operator's own stop ends keeps both, so the next leader finishes the
  frame in flight.
- **The seams for H and I** are in `steps.go`: `guiding.stop` before
  the slew, `centering.center` after it, `guiding.start` before the
  first frame, and `guiding.afterFrame` after each counted frame with
  the count of frames, where a dither goes. Each answers how it guided
  or centered, for the `Imaging` message. E's implementations do
  nothing and say `unguided, not centered`. The operator holds one of
  each, so H and I replace them in `newOperator`.
- **The series** of layer 3 are `astrophotography_frames_exposed_total`
  and `astrophotography_exposure_failures_total`, by namespace and
  target, and `astrophotography_frame_preparation_seconds`.
- **The tests** run the operator against the fake API server, the
  telescope, CCD, and filter wheel simulators of `indi/inditest`, a
  fake camera that plays each exposure, and a fake worker. They cover
  the order of the writes, the selection order and a lost race for a
  claim, a restart during an exposure and after it, a cut connection,
  the end of a reservation during an exposure, a slew, and a check of
  the file, a frame the driver does not finish, saves elsewhere, or
  does not save, a full outbox, and the faults of the worker, the
  mount, and the API server.

The deviations, each with its reason:

- **The saved message comes first.** A probe of the pinned
  `indi-simulators` image on 2026-10-12 recorded `[INFO] Image saved
  to <path>` before `CCD_EXPOSURE` turned `Ok`, which plan 01 left
  open. The runner listens before it exposes and checks the path. A
  driver that sends no message within 5 seconds of `Ok` is checked
  against the worker's list of the outbox.
- **A new reason, `ExposureFailed`.** Plan 01 names no reason for an
  exposure that gives no frame: an `Alert`, no end within the exposure
  plus 2 minutes, a file under another name, or a worker that does not
  answer. The runner keeps the claim, sets `Imaging` `False` with
  `ExposureFailed`, aborts an exposure that did not end, and tries the
  next frame after a minute. `Imaging` stays `False` until a frame
  counts, so a failure that repeats posts one `Warning`.
- **A failed target is skipped by its reservation** until a person
  edits it: `NoFilter` and `SlewFailed` release the target, and the
  runner holds the target's generation in memory so it does not claim
  it again at once. A restart of the operator, or a new reservation,
  tries it once more.
- **The runner syncs each device** with the INDI client's `Sync` once
  at the start of a target's work. A test met a mount whose
  `TELESCOPE_PARK` arrived after `EQUATORIAL_EOD_COORD` on a new
  connection, and the runner read it as unparked.
- **The mount settles for 5 seconds** after it reports the end of a
  slew, because no frame is guided yet.
- **No recount of `pending` from the worker's list at a start.** The
  status survives a restart, and `status.exposure` counts the frame in
  flight once, so the list is read only for that frame. F's reports
  correct `pending` from the outbox.
- **The worker does not run yet.** It is G's. Until it does, the
  runner's check of the outbox's space fails and every exposure ends
  with `ExposureFailed`, and the manual says so.
- **`promotion.go` shares the operator's memo of `Target`s**
  (`targetVersions`), so the runner and the promotion loop each read
  a copy at least as new as the other's last write.

## What G built

Piece G, the `Solver`, the `solver` and `worker` modes, and the
client of a solve from plan 02, was built on 2026-10-12, beside F.

- **The `solver` mode** (`solver/`) reads the header of every
  `index-*.fits` in `--indexes` at its start, writes an engine
  configuration that names each file and `cpulimit 60`, and serves
  `GET /indexes` and `POST /solve`. A solve writes the `.axy` into a
  new directory under `/work`, runs `astrometry-engine -c <config>
  job.axy` there, and answers from the WCS file that the `.axy`'s
  `ANWCS` names, or `{"solved": false}` when the engine wrote none. A
  channel of one token runs one engine at a time, and a request whose
  client goes away leaves the queue. Each run has a wall limit of two
  minutes. The answer's scale and parity come from the `CD` matrix,
  its rotation from astrometry.net's `tan_get_orientation`, and its
  index from the comment `index id:` of the WCS file.
- **The `worker` mode** (`worker/`) answers `POST /extract`, `GET
  /outbox`, and `DELETE /centering/<FRAMEID>`. An extraction reads the
  frame's `NAXIS1` and `NAXIS2`, runs `source-extractor` with the six
  columns of plan 02 and the options of the image's smoke check, and
  `solve-field --just-augment --no-remove-lines --uniformize 0
  --crpix-center --cpulimit 60 -o field -D .` with the scale range
  and, for a radius above 0, `--ra`, `--dec`, and `--radius`. The
  median `FWHM_IMAGE` counts every detection. E's runner reads `GET
  /outbox` for the free space and the frames (`workerclient.go`).
- **`fits/`** reads a header and the numeric columns of a binary
  table. The solver reads index files, `.axy` files, and WCS files
  with it, and the worker reads frames and catalogs.
- **The `Solver` loop** (`solvers.go`, `solverpod.go`) keeps a
  `Deployment` of one pod, `Recreate`, and a `Service`, both
  `<name>-solver`, with the claim mounted read-only at `/indexes`. It
  reads the index list from the pod once for each pod's UID, when the
  pod is `Ready`, and writes `status.endpoint`, `status.indexes`, and
  `Ready`, with one `Event` for each transition.
- **The worker loop** (`workers.go`) keeps `<reservation>-astrophotography`
  for each held reservation that is `Activating` or `Ready` with
  `status.session.outbox`, and deletes every other worker. A pod that
  cannot start posts `AstrophotographyWorkerPending` once for each pod
  and message.
- **The client of a solve** (`platesolve.go`) is the entry point of
  piece I: `o.plateSolve(ctx, solveRequest{Namespace, Reservation,
  Solver, Frame, Center, Scale})` answers a `solveResult` with the
  `solver.SolveAnswer`, the stars and median FWHM, and each attempt's
  radius, star count, outcome, and time. It answers
  `errSolverNotReady` when the `Solver` is missing or not `Ready`, and
  an error when the worker or the solver fails. It ends at the first
  attempt that solves, and at once for a frame with no star.
- **The image** `astrophotography-operator-worker`, from
  `Dockerfile.worker`, is the operator's binary on the `astrometry`
  image, 80.8 MB. Its smoke check,
  `smoke/astrophotography-operator-worker.sh`, runs both modes in
  containers as the caller on a read-only root, and solves the
  astrometry image's synthetic field over HTTP: 109 stars, solved at
  150.000002°, 30.000009°, 2.2353 arcseconds per pixel.
- **The series** `astrophotography_solves_total{result}` and
  `astrophotography_solve_duration_seconds`.

The smoke on the laptop ran both containers of the image with the
lab's 13 index files of 4207 and 4208, and the frame `C_001.fits` of
the CCD simulator, through HTTP, with the hint of the frame's header
(35.627°, 53.820°) and the scale 2.68 ±10%. Both pods answered 0.46 s
after `docker run`.

| Attempt | Extraction | Solve | Result |
|---|---|---|---|
| radius 5° | 0.088 s, 1,675 stars, FWHM 4.93 pixels | 52.2 s | no solution |
| radius 10° | 0.088 s | 0.096 s | 29.6070°, 59.8193°, 2.6815 arcseconds per pixel, rotation 180.0°, negative parity, index 4207 |

Each container held under 9 MiB of memory after the solves. A probe
of the engine with a configured `cpulimit 5` on an `.axy` whose
`ANCLIM` was 60 logged "Decreasing CPU time limit to the engine's
limit of 5 seconds", and gave up after 33 s of wall time; with a
radius of 1° and the limit of 60, it gave up after 43 s. A field that
cannot solve holds the queue for most of a minute.

The deviations, each with its reason:

- **A directory for each job, and the outputs of the `.axy`.**
  `solve-field` writes `ANWCS` and the other outputs as paths under
  its `-D` directory, so `-D /w` wrote `/w/field.wcs` into the `.axy`.
  The worker passes `-D .`, the solver runs the engine in a new
  directory, and it refuses an `.axy` whose outputs leave that
  directory. Plan 02's `/work/<id>.axy` names the input alone.
- **`spec.search` on the `Solver`,** with `radii` (default `[5, 10]`)
  and `blind` (default `true`). Plan 02 tried one radius of 5° and
  then twice the exposure. The frame above missed at 5° and solved at
  10°, so the client widens the radius before the runner of piece I
  lengthens the exposure. The setting is the `Solver`'s, because each
  attempt that cannot solve costs that solver's queue. The CRD also
  holds a `Solver`'s name to 56 characters, so `<name>-solver` fits a
  `Service`.
- **A radius of 0 is a blind solve** in `worker.Hint`, and an
  `ExtractAnswer` with no star has an empty `axy`.
- **`ClaimMissing` comes from the scheduler's message**
  `persistentvolumeclaim "<name>" not found` on the solver's pod. The
  operator watches no claims for the solver.
- **The pod's types are F's** (`promotionobjects.go`), with the fields
  a `Deployment`'s pod adds, and the solver and worker pods run as user
  and group 1000, the drivers', so the worker can delete a centering
  frame. The worker image's tag comes from F's `ownImage`, and
  `ASTROPHOTOGRAPHY_WORKER_IMAGE` overrides it.
- **Two readers of a FITS header.** `promote/` keeps F's, and `fits/`
  holds G's, which also reads a binary table. Moving `promote/` to
  `fits/` is left for a later change.
- **Not proved:** the pods in a cluster, a `NetworkPolicy`, and the
  header's position 6.8° from the drawn stars, which the drill checks
  on a frame of a reservation.

## What H built

Piece H, guiding and dithering in the runner, was built on 2026-10-12,
after E, F, and G, against the PHD2 client of piece C.

- **The connection.** Each runner whose session names a `Guider`
  keeps one connection to PHD2 open for the whole reservation, with
  E's backoff of 1 second that doubles to a minute. Its dialer reads
  the `Guider`'s `status.endpoint` from the watch's store at each dial,
  so a connection that ends follows the `Guider` to a new endpoint, and
  a `Guider` with no endpoint yet is a failed dial. A session with more
  than one `Guider` guides with the first by name.
- **The steps.** Before each slew, the runner sends `stop_capture` when
  PHD2 loops, calibrates, or guides, and waits until PHD2 reports the
  stop. Before each frame, it checks that PHD2 guides a star that
  settled, and when it does not, sends `loop`, waits for the first
  guide frame, sends `find_star` and `guide` with no `recalibrate`, and
  waits for `SettleDone`, with the `Imaging` reason `Guiding`. After
  every `dither.every` counted frames, it sends `dither` and waits for
  `SettleDone`. A dither posts no `Event`.
- **The settle** is 1.5 pixels for 8 seconds with a timeout of a
  minute, the values that `observatory-operator`'s plan 09 drill
  settled with. The wait for a guide is bounded at 10 minutes, because
  PHD2 calibrates first when it holds no calibration, and the drill's
  calibration took 73 seconds.
- **The series** of layer 3 are `astrophotography_dithers_total` and
  `astrophotography_guiding_failures_total`, by namespace and target,
  and `astrophotography_settle_seconds`, by operation, `guide` or
  `dither`.
- **The tests** run the operator against `phd2test` behind a dialer
  that records each address and can switch to a new PHD2. They cover
  the guide before the first frame and its settle, the wait for
  `SettleDone`, `stop_capture` before the slew after a restart, a PHD2
  that does not stop, the dither cadence and its params, a settle that
  fails, no star, no camera, a refused dither, a star lost during an
  exposure, a PHD2 that exits during an exposure, a `Guider` with no
  endpoint, a reservation that ends during a settle, and a session
  with no `Guider`. The total coverage is 98.3%.

The deviations, each with its reason:

- **The seam changed.** E's `guiding.start` ran once, before the first
  frame. Plan 01 asks for `find_star` and `guide` again before the
  next frame after a lost star, so `start` runs before each frame and
  sends nothing while PHD2 guides a settled star. `guiding.open`
  starts the runner's connection before its first step, and
  `guiding.stop` also runs before the slew that follows an edit of the
  coordinates. The centering seam is unchanged.
- **`GuidingFailed` keeps the target.** Plan 01 lists the reason and
  no policy. A cloud is the common cause, and it passes, so a failure
  to guide or to dither sets `Imaging` `False` with `GuidingFailed`,
  keeps the claim, and guides again before the next frame after a
  minute, as `ExposureFailed` does. A start that retries while the
  reason is `GuidingFailed` does not set `Guiding`, so a failure that
  repeats posts one `Warning`. Only a PHD2 that refuses or does not
  finish `stop_capture` within 30 seconds releases the target, because
  the mount does not slew while PHD2 may still guide.
- **The frames wait for the guiding.** A session with a `Guider` takes
  no unguided frame while guiding fails. A long unguided frame trails,
  and grading (plan 06) does not exist to discard it.
- **A PHD2 that does not answer within 30 seconds of a slew** does not
  stop the slew: its pulses go to the mount through the same INDI
  server, and the guide before the next frame reports the failure.
- **A frame with a lost star counts,** as plan 01 states, and gets no
  dither. The guide before the next frame selects a star where it is,
  which moves the lock position too. A dither that falls due while the
  connection to PHD2 is down is skipped the same way.
- **No settle fields on the `Target`.** Plan 01's `dither` has no
  settle, and the values are constants until a drill shows a rig that
  needs others.
- **No `set_exposure`.** PHD2 keeps the exposure of its profile, which
  `observatory-operator` writes.
- **The dashboard** of the monitoring component does not show the
  three series of guiding yet.

Open after H:

- **`dither` and a settle that fails** have met only the fake. The
  drill's step 9 is their first check against PHD2.

## What I built

Piece I, centering in the runner of plan 02, was built on 2026-10-12,
on E's seam and G's client of a solve, while H built guiding in the
other seam.

- **The step** is `plateCentering` (`centering.go`), which
  `newOperator` gives every runner in place of E's step that did
  nothing. A target with no `spec.centering` is not centered, as
  before. Otherwise `Imaging` turns `True` with the reason `Centering`
  and stays there through every round, so a centering posts one
  `Event`. Each round takes a centering frame, solves it with
  `plateSolve`, and compares the solved center with the target, both
  J2000, with `sky.Separation`. Within `centering.tolerance`, the
  runner starts the frames. Otherwise it sets `ON_COORD_SET` to
  `SYNC`, writes the solved center converted with `sky.ToDate` to
  `EQUATORIAL_EOD_COORD`, sets `ON_COORD_SET` back to `TRACK`, and
  slews to the target again with the same conversion, then waits 5
  seconds as after the first slew. `slew.go` gives that slew to both
  as `goTo`.
- **The centering frame** (`centeringframe.go`) goes through E's
  `writeFrame`: the upload to
  `/outbox/<Camera>/<Reservation>/_centering/<FRAMEID>`, the light
  frame type, the binning, gain, and offset of the first sequence,
  the filter `centering.filter` or the first sequence's, and
  `INDI_CLEAR`, the session's and the target's keywords, `OBJECT`, and
  a new `FRAMEID`. E's `exposeAndWait` exposes it and checks the saved
  path. The runner records no `status.exposure` for it, so a restart
  never counts it. After the solve, the runner sends the worker
  `DELETE /centering/<FRAMEID>`, and a delete that fails is logged.
- **The plate scale** is 206.265 × `CCD_PIXEL_SIZE` of the driver's
  `CCD_INFO` × `HOR_BIN` of its `CCD_BINNING`, read after the frame's
  settings are written, / the focal length of the train's tube in
  `status.session.tubes`.
- **`status.centering`** is written once when a centering ends, in
  the target's own write: the reservation, the time of the last round,
  the rounds, the last solution, with its center as `HH:MM:SS.s` and
  `±DD:MM:SS.s` from the new `sky.FormatRA` and `sky.FormatDec`, and
  the error, rounded to a tenth of an arcsecond. The `Imaging` message
  then says, for example, `unguided, centered within 12.3 arcseconds
  in 2 rounds`.
- **The series** are `astrophotography_centering_rounds`, a histogram
  of the rounds of each centering that ended with a solution or gave
  up, and `astrophotography_centering_error_arcseconds`, the last
  error, by namespace and target.
- **The tests** run the runner against the fake API server, the
  simulators of `indi/inditest`, the fake camera, and the fake worker,
  which now hands an extraction or a solve to the fake peers of G and
  answers the delete. A Ready `Solver` comes from G's loop and a ready
  pod. The fake solver answers each round with a center that the test
  scripts. They cover a target centered in the first round and in the
  third, the one `Event`, the order and the values of the sync and the
  slew, a frame that does not solve at 5 s or at 10 s, the rounds that
  run out, a camera with no pixel size, a session with no tube, a
  worker that fails, a camera that refuses the exposure, a mount that
  refuses the sync, an exposure that does not read, a centering filter
  the wheel lacks, a `Solver` that is not `Ready` and one that goes away
  between rounds, the keywords, file, and hint of a centering frame,
  and a delete that fails. The coverage is 98.4%.

The deviations, each with its reason:

- **A `Solver` that is not `Ready` images the target uncentered.**
  Plan 02 gives no rule. The runner checks the `Solver` before the
  first frame and on every solve, and when it is missing or not
  `Ready`, it images the target where the slew left the mount. The
  `Imaging` message says `not centered because Solver <name> is not
  Ready`, and the reservation gets the `Warning`
  `AstrophotographyNotCentered`, a new reason. Frames a few arcminutes
  off are still frames of the target, and a reservation that starts
  while the solver's pod restarts would otherwise lose the target for
  the night.
- **Twice the exposure comes after G's ladder of radii.** Plan 02
  tries once more with twice the exposure when a frame does not solve.
  G's client tries each radius of the `Solver`'s `spec.search` and
  then a blind solve on each frame, so the longer frame is the last try
  of a round, and a round whose two frames do not solve ends the
  centering with `CenteringFailed`.
- **The pixel size comes from the driver**, not from the camera's
  `status.info`. Both hold the driver's `CCD_INFO`; the runner already
  holds the connection, and the driver also holds the binning the
  runner just wrote.
- **Each failure is `CenteringFailed`**, and the runner leaves the
  target for the rest of the reservation, as for `SlewFailed`: a frame
  that does not solve, rounds that run out, an unknown plate scale, a
  worker or a solver that answers an error, a centering frame the
  camera fails, and a sync the mount refuses. A lost INDI connection
  and the end of the runner pass through as they do for a slew, and a
  centering filter the wheel does not hold fails with `NoFilter`, as a
  sequence's does.
- **The settings of a centering frame.** Plan 02 names the exposure
  and the filter. The binning, gain, and offset are the first
  sequence's, so the frame matches the first frames of the target.

Open after I:

- **A slew for edited coordinates is not centered.** `work.next`
  slews again when a person edits a target's coordinates during the
  night, and calls no centering. Centering runs after the slew of each
  claim, which includes the claim after a restart or a lost
  connection. Closed when H and I were merged: `work.aim` stops the
  guiding, slews, and centers, after each claim and after an edit of
  the coordinates.
- **A centering frame that a crash leaves stays in `_centering`.** The
  runner deletes each frame after its solve. A frame whose solve a
  restart interrupts is never deleted, and the worker's list leaves it
  out, so only its space shows it.
- **The drill** checks plan 02's two rounds and 30 arcseconds against
  the simulators, and the sync's effect on the telescope simulator,
  which the fakes here do not model.

## The drill of the first session

The drill runs on the QEMU dev cluster's `node-1` alone, with 4 GB of
memory, its state disk of 6 GB, and its pool of claims of 2 GiB. The
images reach it through the lab registry (`registry/push.sh`):
`observatory-operator`, `astrophotography-operator`,
`astrophotography-operator-worker`, `indi`, `indi-simulators`,
`indi-phd2`, and `weston`. The `Makefile` of the dev cluster records
2.9 GB of the state disk in use with the simulators, PHD2, and weston;
the worker image adds about 100 MB.

1. Apply both deploy bases, and `observatory-operator`'s
   `examples/simulators.yaml` with no `Reservation`, in `observatory`.
2. Create three `local-path` claims: `frames` (the outbox, 1 GiB),
   `archive` (1 GiB), and `astrometry-indexes` (512 MiB).
3. Fill `astrometry-indexes` with the 13 files of plan 02, 236 MiB,
   from the laptop through a pod that mounts it, with `tar` over
   `kubectl exec`.
4. Apply `Solver lab`, and check `Ready`, and `status.indexes` with 4207
   and 4208.
5. Apply `Target m42` with short frames: Luminance 10 s × 3, and Red,
   Green, and Blue 10 s × 2 each, `dither.every` 2, centering on `lab`
   with a tolerance of 30 arcseconds, the archive `archive` under
   `astro`, and `Continuous` promotion.
6. Reserve `east` with the holder `astrophotography-operator` and the
   outbox `frames`. Check that the worker starts on `node-1`.
7. Follow `Imaging` through `Slewing`, `Centering`, `Guiding`, and
   `Exposing`, with one `Event` for each. Record each step's time.
8. Check `status.centering`: the error within 30 arcseconds in at most
   two rounds, and the solve's duration.
9. Check PHD2: `SettleDone` after `guide`, and after each dither.
10. Read the archive from a pod that mounts it read-only: nine frames
    under `astro/M42/<night>/<filter>/`, each with a unique `FRAMEID`,
    `OBJECT = 'M42'`, the session's `OBSERVER` and `TELESCOP`, and the
    right `FILTER` and `EXPTIME`. Check that `catalog/m42.jsonl` has
    nine lines whose hashes match the files, and that the outbox's
    directory of `m42` is empty.
11. Check `Complete` `True` and the progress `9/9`.
12. Delete the operator's pod during an exposure. Check that the frame
    is counted once and that no exposure is lost or repeated.
13. Delete a promotion pod during a copy. Check that the next `Job`
    leaves no `.part` file and records each frame once.
14. Apply a second target whose archive is `frames`, the outbox
    itself. Check that its frames move by rename into its `path`.
15. End the reservation. Check that the worker goes, the mount parks,
    and the reservation reaches `Released`.

The drill records, for the plan that closes: the time of each step,
the rounds and error of each centering, the time from the end of one
exposure to the start of the next, the time from a frame's end to its
line in the catalog, the memory of the worker and the solver pods, and
the disk in use.

## The early run

On 2026-10-12, before H and I were built, a session of E, F, and G ran
on the QEMU dev cluster, to find the faults that only a cluster shows
before the drill of the first session runs. The cluster was `node-1`
alone, with 4 GB of memory and a state disk of 6 GB. The three images
were built from adde1c25, and then again with the fix below, and
reached the lab through the lab registry. Each `Deployment` named its
image by digest, because the node held older `:dev` images and
`IfNotPresent` keeps them, and the operator's
`ASTROPHOTOGRAPHY_WORKER_IMAGE` named the worker's digest.
`observatory-operator` ran the same commit, with its CRDs applied
again. The observatory was the example in `observatory`, with
`Guider east`. The runner guided nothing and centered nothing, and the
`Imaging` message said `unguided, not centered`.

The `Target` `m42` had the example's coordinates, train, `dither`, and
`centering`, and two sequences of two frames of 5 s each, Luminance
and Red. Its archive was `archive` under `astro`, with the format
`{object}/{night}/{filter}/{object}_{filter}_{exposure}_{n}` and
`Continuous` promotion. The `Reservation` `east-tonight` held `east`
for `astrophotography-operator`, with the outbox `frames`.

| Check | Result |
|---|---|
| Copy the 13 index files into `astrometry-indexes` with `tar` over `kubectl exec` | 236 MiB in 1.5 s. The MD5 sum of each file matched the laptop's. |
| Apply `Solver lab` | `Ready` 4 s after creation, with `Loaded` and the message `13 index files of the series 4207, 4208`. `status.indexes` listed 4207 with 12 files and 4208 with 1 file. |
| Reserve `east` | `Ready` 40 s after creation. `Activation` took 24 s, and the camera's cooling to -10 °C took 20 s of that. The worker `east-tonight-astrophotography` started on `node-1` in the same second as `east-telescope`. |
| The first slew | `SlewFailed` 30 s after `Slewing`, with `Telescope Simulator.TELESCOPE_TRACK_STATE: context deadline exceeded`. The fix follows this table. |
| The slew with the fix | `Exposing` 22 s after `Slewing`, and 5 s of that was the settle time. |
| Expose four frames | The frames started 6.95 s, 7.02 s, and 7.10 s apart, so the runner took about 2 s between the end of one exposure and the start of the next, and the change from Luminance to Red took no longer. |
| Promote each frame | Each frame had its own `Job`, `m42-promote-1` to `-4`. From the end of a frame to the end of its `Job` took 6 s for the first and 3 s for each other, and `archived` rose within 1 s after the `Job` ended. `Complete` turned `True` with `Archived` and `4/4` 3 s after the last exposure ended. |
| Read the archive from a `busybox` pod that mounts it read-only | Four files of 2,629,440 bytes, such as `astro/M42/2026-10-12/Luminance/M42_Luminance_5s_0001_40d7a8a9d7f3.fits`, owned by user and group 1000. Each name ends in the last 12 digits of its `FRAMEID`. |
| Read each header | `OBSERVER = 'Simulated Observer'`, `TELESCOP = 'East refractor'`, `OBJECT = 'M42'`, a UUIDv7 `FRAMEID` that matched its name, the sequence's `FILTER`, and `EXPTIME = 5`. |
| Read `astro/catalog/m42.jsonl` | Four lines, one for each `FRAMEID`. The `sha256` of each line matched `sha256sum` of its file. |
| Read the outbox | The directories of `m42` held no file. |
| End the reservation | `Released` 56 s after the delete. The worker went, and `Mount east` parked in 4 s. |

The fix: INDI's telescope base answers `TRACK_ON` with
`TELESCOPE_TRACK_STATE` `Busy`, and keeps it `Busy` for as long as the
mount tracks. The `Mount`'s status read `{"state":"Busy","value":"TRACK_ON"}`
while the runner waited. The runner turned tracking on with `Settle`,
which waits for a state that is not `Busy`, so it waited until the
limit of 30 s, and the target failed. `indi` gained `Client.Accepted`,
which ends on the first update after the send that carries the values
sent, in any state but `Alert`, and `readyMount` turns tracking on with
it. A test of `indi` and a test of the slew, with a fake mount that
answers `TRACK_ON` with `Busy`, failed before the fix. A second
reservation, which started the mount with `TRACK_OFF`, reached
`Exposing` 22 s after `Slewing` and archived its one frame.

A solve through the worker and the solver ran on one archived frame
after the session. The frame went into the outbox's `_centering`
directory, the only directory beside a target's that the worker
extracts from, and port forwards reached both `Service`s. The hint was
the header's position, `RA` 83.81965° and `DEC` -5.393677°, and the
train's scale, 206.265 × 5.2 µm / 480 mm = 2.2345 arcseconds per pixel,
±10%.

| Step | Result |
|---|---|
| `POST /extract` | 0.12 s, 2,614 stars, median FWHM 6.26 pixels |
| `POST /solve`, radius 5° | 0.09 s: 83.82031°, -5.39357°, 2.2345 arcseconds per pixel, rotation 180.0°, negative parity, index 4207 |
| `POST /solve`, blind | 0.10 s, the same solution |
| `DELETE /centering/<FRAMEID>` | 204, and the file was gone |

So on this frame, the header's position and the drawn stars agree
within 2.4 arcseconds, and the 6.8° of G's smoke did not occur. The
solution is 10.9 arcseconds from the `Target`'s J2000 coordinates, and
the header's position is 12.7 arcseconds from them. That difference
comes from the runner's conversion to the equinox of the date and the
driver's conversion back, and the run did not establish which one
differs. The solver and the worker each held under 7 MiB of memory
after the solves.

Found and not fixed:

- **The `Solver`'s index list is read through its `Service`.** When the
  operator rolled out a new worker image, the new solver pod was
  `Ready`, and the read of `GET /indexes` through `lab-solver` timed
  out after 30 s, so the `Solver` was `PodNotReady` for 30 s. On the
  first `Solver`, the first three reads were refused within 5 ms, and a
  read 2 s later answered. Each time, the `Service` did not yet route
  to the new pod. The list is kept by the pod's UID, so a read from
  the pod's own address would answer for that pod alone.
- **The `INDEXES` column shows one series.** `kubectl get solver`
  printed `4207`, and the set-up guide shows `4207,4208`. The API
  server prints only the first value of a column whose JSONPath gives
  several, so `.status.indexes[*].id` shows the first series.
  `equipment-operator`'s `CECBus` has two columns of the same kind.
- **"moved 1 frames".** The `Promoted` `Event` and the operator's log
  line say `1 frames`.
- **A `promotion: conflict` line** appeared in the log once in each
  session, and the next try 1 s later succeeded.

At the end, the `Solver` `lab`, its claim `astrometry-indexes`, the
claim `archive` with its five frames and two catalogs, and both
operators stayed installed. Both reservations were deleted, and every
device of `east` was `Idle`.

## The first session

On 2026-10-12, the drill of the first session ran every piece together
on the QEMU dev cluster's `node-1` alone, with 4 GB of memory and a
state disk of 6 GB. Its clock times are in UTC, from 01:39 to 02:05 on
2026-10-13. A person declared a `Target` and a `Reservation`, and with
no other action `observatory-operator` started the telescope and PHD2,
and `astrophotography-operator` slewed, centered with a plate solve,
guided, exposed ten frames with dithers, and moved each frame to the
archive, where its hash matched its catalog line. The drill found one
fault, which it fixed, and five it did not fix. [Fixed after the
session](#fixed-after-the-session) records the faults fixed later.

The lab was the one of [the early run](#the-early-run): the example
observatory in `observatory`, with `Guider east`, the `Solver` `lab`
with 13 index files of the series 4207 and 4208, and the claims
`frames`, `archive`, and `astrometry-indexes`. The images were built
on the laptop from 0af20254 and reached the lab through the lab
registry, and each `Deployment` and `ASTROPHOTOGRAPHY_WORKER_IMAGE`
named its digest. `observatory-operator`'s image had the same digest
as in the early run, which already held the fix of `TRACK_ON`. After
the fix below, the two images of this operator were built again from
016fe677, which reads the index list from the solver pod's address,
with the fix in the working tree. The checks of centering by a sync,
the restart, and the second reservation ran on those images.

### The session

The `Target` `m42-first` had the example's coordinates and train, four
sequences of 10 s frames, Luminance × 3, Red × 3, Green × 2, and
Blue × 2, `dither` every 2 frames by up to 5 pixels, and `centering`
with `Solver lab` and a tolerance of 30 arcseconds. Its archive was
`archive` under `first`, with the format
`{object}/{night}/{filter}/{object}_{filter}_{exposure}_{n}` and
`Continuous` promotion. The `Reservation` `east-first` held `east` for
`astrophotography-operator`, with the outbox `frames`. Both were
applied at 01:39:41.

| Check | Result |
|---|---|
| Upgrade both operators | The new pod of this operator took the `Lease` 10 s after its start, when the old pod released it. The `Solver` rolled its pod onto the new worker image and was `Ready` again 12 s after the rollout, with 13 files of 4207 and 4208. |
| Reserve `east` | `Ready` 40 s after creation. `StartSite` took 6 s, `PowerOn` 2 s, `StartDevices` 3 s, `Configure` 1 s, `Activation` 24 s, and `StartGuider` connected PHD2 to its camera and mount in 4 s. The worker `east-first-astrophotography` started on `node-1` 6 s after the reservation's creation. |
| Slew | `Imaging` turned `True` with `Slewing` in the second that the reservation was `Ready`, and `Centering` 19 s later, 5 s of which was the settle time. |
| Center | One round of 7 s: a 5 s centering frame, 2,260 stars, solved at the radius of 5° in 209 ms as the operator measured it, and in 0.088 s as the solver's series measured it. The solution was 05:35:16.7, -05:23:36.9, rotation 180.0°, 2.2345 arcseconds per pixel, 12.8 arcseconds from the target, within the tolerance of 30, so the runner sent no sync. The worker deleted the centering frame, and `_centering` held no file after the session. |
| Guide | `Guiding` came 7 s after `Centering`. PHD2 selected a star at (601.9, 544.6), calibrated for 73.6 s, from `StartCalibration` to `CalibrationComplete`, began guiding, and reported `SettleDone` with status 0 after 11.8 s and 10 frames, none dropped. `Exposing` came 2 s after `SettleDone`. The series `astrophotography_settle_seconds` of the guide recorded 86.6 s, from `loop` to `SettleDone`. |
| Dither | Five dithers, after frames 2, 4, 6, 8, and 10. PHD2 moved the lock position by (-1.06, 2.83), (2.98, 4.12), (-3.02, -1.65), (2.68, -2.22), and (0.54, -0.23) pixels, and settled in 11.2, 13.4, 12.0, 12.0, and 10.2 s, each with status 0 and no frame dropped. |
| Expose ten frames | The starts in `DATE-OBS` were 11.6 to 12.1 s apart with no dither, so 1.6 to 2.1 s passed between the end of one exposure and the start of the next, and 23.2 to 25.4 s apart across a dither. The change of filter took no longer. `astrophotography_frame_preparation_seconds` averaged 1.93 s. |
| Promote each frame | Ten `Job`s, `m42-first-promote-1` to `-10`, each created in the second the runner counted its frame and complete 2 to 3 s later. `archived` rose in the same second. `Complete` turned `True` with `Archived` and `10/10` 2.3 s after the last exposure ended. |
| Read the archive from a `busybox` pod that mounts it read-only | Ten files of 2,629,440 bytes under `first/M42/2026-10-12/<filter>/`, owned by user and group 1000. Each header had `OBSERVER = 'Simulated Observer'`, `TELESCOP = 'East refractor'`, `OBJECT = 'M42'`, the sequence's `FILTER`, `EXPTIME = 10`, and a UUIDv7 `FRAMEID` whose last 12 digits ended the file's name. The ten `FRAMEID`s were unique. |
| Read `first/catalog/m42-first.jsonl` | Ten lines, one for each `FRAMEID`, and the `sha256` of each line matched `sha256sum` of its file. |
| Read the outbox | The directories of `m42-first` held no file. |
| PHD2 over the session | The `Guider`'s status gave a total RMS of 1.03 arcseconds, 0.80 in right ascension and 0.64 in declination, over 100 steps, at 2.475 arcseconds per guide pixel, with a star of HFD 2.29 and SNR 126. |
| The series of this operator | `frames_exposed_total` 10, `frames_archived_total` 10, `dithers_total` 5, `promotion_bytes_total` 26,294,400, every `promotion_failures_total` 0, `solves_total{result="solved"}` 1, `centering_rounds` one centering of 1 round, `centering_error_arcseconds` 12.8. `observatory-operator` counted no step failure. |
| Memory | The worker held 6 MiB, the solver 5 MiB, PHD2 103 MiB, the INDI server 25 MiB, and the imaging camera's driver 10 MiB. The node's image filesystem had 2,234 MiB free before the session and 2,097 MiB after it. |

### Centering by a sync

The first session centered within the tolerance in its first round, so
it sent no sync. A second target, `m42-sync`, of two 5 s Luminance
frames at the same coordinates, asked for a tolerance of 5 arcseconds
and 3 attempts, so the 12.8 arcseconds of the first round needed a
sync. Its archive was `archive` under `sync`.

On the images of 0af20254, the first round solved 2,557 stars in
193 ms, and the sync failed 30 s later. The target turned `False` with
`CenteringFailed` and the message `syncing the mount: Telescope
Simulator.EQUATORIAL_EOD_COORD: context deadline exceeded`. The fix
follows.

On the images with the fix, the operator's restart took `m42-sync`
again. It centered in 2 rounds: the first solved 3,169 stars in 223 ms
and synced, and the second solved 2,327 stars in 195 ms at 05:35:17.3,
-05:23:28.0, 0.6 arcseconds from the target. So the sync moved the
simulator's pointing as plan 02 expects, and the 12.8 arcseconds of
the first session came from where the mount reported that it pointed,
not from the solve. No record holds the error of the first round,
because `status.centering` and the series keep only the last round,
and the log line of a solve gives no error. PHD2 kept its calibration
and settled 10.0 s after `guide`, in 9 frames, and both frames reached
the archive.

The fix: the telescope simulator feeds a sync to an alignment model,
and answers it with where the model computes that the mount points.
A raw INDI client sent `SYNC` and then the position 5.6101619 h,
-5.3751266°, which was the mount's own position moved by a few
arcseconds. The driver logged `Sync replaced 1 older alignment
point(s) at the same position.` and `Sync is successful.`, and
answered `EQUATORIAL_EOD_COORD` `Ok` twice: first with the old
position, and 48 ms later with 5.6101055 h, -5.3751266°, 3 arcseconds
in right ascension from the position sent. It never reported the
values sent. The runner wrote the position with `Settle`, which waits
for the values sent, so the sync waited until the limit of a setting,
30 s. `syncTo` now waits for the position's write with `Answered`,
which ends on the driver's first answer that is not `Busy` and fails
on `Alert`, and keeps `Settle` for the two writes of `ON_COORD_SET`.
A test whose mount answers a sync 2 arcseconds east of the position
sent failed with the drill's message before the fix (`centering.go`,
`indiwrites.go`, `centering_test.go`).

### A restart during an exposure

The target `m42-restart` had three Red frames of 20 s, no dither, and
no centering, with the archive `archive` under `restart`. The drill
deleted the operator's pod 9.7 s into the second exposure.

| Check | Result |
|---|---|
| The new leader | The new pod held the `Lease` 0.5 s after the delete. |
| The frame in flight | 12.8 s after the delete, the new runner logged that the frame `...5becc7ce1bb3` in flight before the restart was in the outbox, counted it, and started its promotion `Job`. It then sent `stop_capture` to PHD2, slewed, and guided again, with `SettleDone` 10.0 s after `guide`, and took the third frame 14 s after `Guiding`. |
| The counts | `3/3`, `archived` 3, `Complete` `True`. The archive held three files and the catalog three lines with three unique `FRAMEID`s, and each hash matched. No exposure was repeated or lost. |

### The second reservation

The `Reservation` `east-second` imaged two targets, to check steps 13
and 14 of the drill. It was `Ready` 40 s after creation. PHD2
calibrated again, because its pod was new, and the first frame came
88 s after `Guiding`.

| Check | Result |
|---|---|
| Archive on the outbox claim: `m42-rename`, two Green frames of 2 s, archive `frames` under `kept` | Each frame moved to `kept/M42/2026-10-12/Green/` on the same claim, in a `Job` of 3 s. Both hashes matched the catalog `kept/catalog/m42-rename.jsonl`, and the target was `Complete` with `2/2`. |
| Delete a promotion pod during a copy: `m42-batch`, eight Luminance frames of 2 s, archive `archive` under `batch`, `AfterReservation` | The `Job` `m42-batch-promote-1` started for 8 frames in the second that the reservation was `Released`. The drill force-deleted its pod as soon as the API server reported it `Running`. The pod had already finished: the `Job` recorded `succeeded: 1` within 1 s, and the archive held all eight frames, eight catalog lines, and no `.part` file. So the copy that step 13 asks to interrupt was not reached, and that check did not run. The deleted pod lost the report, which is the second fault below. |
| End the reservation | `Released` 56 s after the delete. |

### The release

The drill deleted `east-first` at 01:54:23, after the restart check.
`Abort` stopped PHD2, which was guiding, in 1 s. `Deactivation` took
49 s, and the mount parked in 3 s of it, after the camera warmed. The
reservation was `Released` 56 s after the delete. The worker went, and
every device of `east` was `Idle`.

### Found and not fixed

- **A sync that fails records no round.** `rounds` returns the error
  of `syncTo` or `goTo` without `end`, so `status.centering` and the
  series of rounds have no record of a centering that failed there.
  `m42-sync`'s first try left `status.centering` empty.
- **The frame in flight across a restart is not in
  `astrophotography_frames_exposed_total`.** The new process counts it
  in the status and not in the series, which read 1 for the three
  frames of `m42-restart` in the new process.
- **A `promotion: conflict` line** appeared in the log three times in
  the session, and each next try 1 s later succeeded, as in the early
  run.

At the end, both operators, the `Solver` `lab`, the claims, the
targets, and the archived frames stayed in place. Both reservations
were deleted, and every device of `east` was `Idle`.

### Fixed after the session

- **A promotion `Job` whose pod is deleted after it succeeded stops
  promotion for its target.** The report is the termination message
  of the pod, and `resultOf` waits for the pod of a `Job` that
  succeeded, because the watches of `Job`s and pods deliver their
  changes apart. A pod that is deleted does not come back, so the loop
  waits for ever: `m42-batch` held `archived` 0 and `pending` 8 for 5
  minutes with all eight frames in the archive, and no `Job` started.
  A person, the garbage collector, or a lost node can delete the pod.
  Deleting the `Job` recovered the target: the loop created `Job` 1
  again, which found the eight frames in the catalog and moved
  nothing, and the target was `Complete` with `8/8` 3 s later. The
  catalog still held eight lines. Which rule fits, a time limit on the
  wait or a read of the pod from the API server, is a decision for
  plan 03.
  Fixed on 2026-10-13 by the rule that plan 03 records in [the
  report](03-promotion-to-the-archive.md#the-report): the loop waits
  for a report only while its store holds the pod. When the pod of a
  `Job` that succeeded is gone, the loop deletes the `Job`, posts the
  `Normal` `Event` `PromotionReportLost` on the target, and starts the
  same number again, which finds the frames in the catalog and
  reports the counts (`promotion.go`, `promotionreport_test.go`).
- **A dither after the last frame.** The runner dithers after every
  `dither.every` counted frames, including the last frame of the
  target, so `m42-first` waited 10.2 s for a settle that no frame
  used before `SequencesDone`. `Complete` turned `True` 7 s before
  `Imaging` left `Exposing`.
  Fixed on 2026-10-13: the runner calls `afterFrame` before the next
  frame of the target, not after the frame it counted, so the frame
  that completes the work, or a frame before a slew to new
  coordinates, gets no dither (`imaging.go`, `dither_test.go`).
