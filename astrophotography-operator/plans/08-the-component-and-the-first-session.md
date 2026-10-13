# 08, The component and the first session

Proposed and designed on 2026-10-12. Pieces B and D of stage 1 were
built on 2026-10-12, and [what they built](#what-b-and-d-built) lists
them with each deviation. The rest is not built. This plan builds
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
