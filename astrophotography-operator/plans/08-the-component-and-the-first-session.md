# 08, The component and the first session

Proposed and designed on 2026-10-12. Pieces B and D of stage 1 were
built on 2026-10-12, and [what they built](#what-b-and-d-built) lists
them with each deviation. Piece F of stage 2 was built on 2026-10-12,
and [what F built](#what-f-built) lists it the same way. The rest is
not built. This plan builds
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
