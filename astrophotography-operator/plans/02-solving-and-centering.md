# 02, Solving and centering

Proposed on 2026-10-12, and designed on 2026-10-12. Built on
2026-10-12 as pieces A, G, and I of [plan
08](08-the-component-and-the-first-session.md), which lists what each
piece built and its deviations from this design. The drill of plan 08
has not run, so no centering has run against the simulators in a
cluster. [The design](#the-design) answers what the design conversation left open,
from solves of CCD simulator frames on the laptop with the
astrometry.net and SourceExtractor of Ubuntu 26.04.

## The problem

A mount's slew lands near a target, not on it. The operator solves a
frame to find where the telescope points, and nudges the mount until
the target is centered, usually in two or three rounds. Solving reads
a frame's stars, the frames are in the outbox on the machine at the
pier, and the solver's index files run to gigabytes. Measured on a
cluster in `observatory-operator` plan 03, the link between nodes
carried 76 Mbit/s, so moving a 52 MB frame for each solve would cost
seconds each round.

## Decisions

- **astrometry.net is the solver.** Its software is BSD and GPL, its
  Tycho-2 index files are BSD, its 2MASS index files are GPL, and the
  2MASS catalog is public domain, so the project can build it into an
  image and mirror its index files.
- **Extraction runs beside the frames, and solving runs once in the
  cluster.** astrometry.net already divides its work at the `.axy`
  file: extracting the stars reads the whole frame and writes a list of
  star positions with hints such as the field width, a few kilobytes,
  and `astrometry-engine` solves that list with no pixels. So a worker
  pod for each active reservation mounts the outbox and extracts, and
  one solver in the cluster holds the index files once. Only kilobytes
  cross the network.
- **SourceExtractor does the extraction, not `image2xy`.** `image2xy`
  writes only positions and flux. SourceExtractor also measures each
  star's size and shape, so its one catalog feeds both the solve and
  the measurements that grading uses.
- **One `astrometry` image holds both**, a new pinned image component
  like `indi`. astrometry.net and SourceExtractor are in Debian, so it
  can build from the same dated snapshot as the other bases.
- **A person declares a `Solver`** with a claim for the index files.
  The operator fetches the index files that the optical trains need,
  which follow mostly from each train's field width. A person can also
  fill the claim with index files they already have, which lets a
  night run with no network at all.
- **Centering converts between epochs.** The mount reports JNow and the
  solver reports J2000, about 20 arcminutes apart for M42 in 2026.

## Considered and set aside

- **ASTAP as the default.** It is fast and small, under MPL-2.0, but
  it reads the whole image, so it would solve beside the frames with a
  database on every pier machine. Its star databases, derived from
  Gaia, state no license, so the project does not mirror them. ASTAP
  stays an option a person could choose, fetching its database
  themselves.
- **A `Job` for each solve.** Each round of centering would wait for a
  pod to start.
- **A solver container in the camera's pod.** It puts this operator's
  container inside `observatory-operator`'s pod.


## What the probes found

On 2026-10-12, `indiserver` from the pinned `indi-simulators` image
(`20261005-5`) ran in Docker on the laptop with the telescope, CCD, and
focuser simulators. `SCOPE_INFO` was the example's `east-refractor`, a
focal length of 480 mm and an aperture of 80 mm, and the mount slewed
to the J2000 coordinates of M42, written as if they were JNow. The
laptop's own astrometry.net 0.98 and SourceExtractor 2.28.2, from
Ubuntu 26.04, solved the frames.

- **The simulator's stars solve.** A blind `solve-field` of a 5 s frame
  with only a scale hint solved in 1.2 s of wall time, with a
  maximum RSS of 65 MB: center 83.4883°, −5.4123°, 2.233 arcseconds per
  pixel, a field of 47.68 by 38.14 arcminutes, rotation 180.0°, and
  negative parity. The header's own `OBJCTRA` and `OBJCTDEC` were the
  same point, so the simulator draws the stars of the J2000 position
  that its mount reports.
- **The epoch matters.** The mount took the J2000 numbers as JNow, and
  the frame's center was 20 arcminutes west of M42, as the decisions
  predicted. The operator converts before it slews.
- **The GSC is in the image, and it is complete.** The image holds
  `/usr/share/GSC` with all 9,537 region files of the Guide Star
  Catalog, 304 MB. The earlier warning "No stars found in field --
  check gsc catalog coverage for this region" came from a camera with
  no focal length: with `SCOPE_INFO` at 0, the driver sent that
  warning with "Telescope focal length is missing", and drew no stars.
  `observatory-operator`'s `Configure` writes `SCOPE_INFO` from the
  train's tube, so a camera in a reservation draws stars. Nothing
  blocks solving in the lab.
- **The field takes two scales of index.** The plate scale is 206.265 ×
  5.2 µm / 480 mm = 2.235 arcseconds per pixel, so the field is 47.7 by
  38.1 arcminutes. Each set of index files alone, with the scale hint:

  | Index files | Result |
  |---|---|
  | 4207, 2MASS, 22 to 30 arcminutes | Solved in 1.3 s |
  | 4208, 2MASS, 30 to 42 arcminutes | Solved in 1.1 s |
  | 4209, 2MASS, 42 to 60 arcminutes | No solution in 7.5 s |
  | 4107, Tycho-2, 22 to 30 arcminutes | Solved in 1.0 s |
  | 4108, Tycho-2, 30 to 44 arcminutes | Solved in 0.9 s |
  | 4109, Tycho-2, 44 to 60 arcminutes | No solution in 7.7 s |

  A 4209 quad is wider than the field's short side, so it cannot fit.
- **Extraction is fast and small.** SourceExtractor read the
  1280 by 1024 frame and wrote a catalog of 14,400 bytes in 0.03 s,
  with a maximum RSS of 28 MB. `solve-field --just-augment` turned the
  catalog into an `.axy` of 11,520 bytes in 0.01 s.
- **`solve-field` runs Python unless it is told not to.** By default,
  `--just-augment` ran `python3 -m astrometry.util.removelines` and
  `astrometry.util.uniformize`. With `--no-remove-lines` and
  `--uniformize 0` it ran no other program, and the `.axy` still
  solved. So the image needs no Python.
- **`astrometry-engine -f -` keeps running.** It read each path that
  arrived on standard input, solved each `.axy` with the RA and Dec
  hint, and stayed at 8.5 MB of RSS over three jobs. It read the
  metadata of its 13 index files once, at its start. It writes no file
  for a field it does not solve, so a reader cannot tell a failed job
  from a slow one by the files alone.
- **The engine reopens a job's `.axy` many times while it works on it,
  and opens the next `.axy` only after the job ends.** Under
  `inotifywait`, the first open of a second file came after the last
  open of the first. A second `.axy` whose `ANRUN` is `F` was read and
  skipped at once.
- **A new engine for each job costs little.** One run of
  `astrometry-engine` for one job took 0.06 s with the 13 files of 4207
  and 4208, and 0.56 s with all 205 index files of the laptop, 9.9 GB.
  The engine that stays running saves at most that much for each
  solve.
- **The `.axy` names its outputs.** `ANWCS`, `ANSOLVED`, and the others
  are paths relative to the engine's directory, from the `-o` that
  built the `.axy`. With `--crpix-center`, `CRVAL1` and `CRVAL2` of the
  solution are the center of the frame.

## The design

### The `astrometry` image

`astrometry` is a new pinned image component at the top of the
repository, beside `ffmpeg`. It builds a closure on `scratch` of
`astrometry-engine`, `solve-field`, and `source-extractor`, with every
library the loader resolves for them, from the Debian snapshot that the
other bases on Debian use, `20260928`. Debian 13 holds astrometry.net
`0.97+dfsg-2` and SourceExtractor `2.28.2+ds-1`, and `[package.upstream]`
names both, so each becomes a label of the image. The probes above ran
0.98 from Ubuntu; the smoke check of the image proves the same
options on 0.97.

- **Debian, not the Ubuntu snapshot of `indi`.** The image shares
  nothing with the INDI images, and the other bases that hold
  libraries from a distribution use Debian. A bump of it follows the
  `bump-components` skill.
- **The size.** On the laptop, the three programs and their libraries
  are 88 MB, and OpenBLAS, which SourceExtractor loads through LAPACKE,
  is 38.6 MB of that. `solve-field` links `cairo` for its plots, so the
  closure carries it even though the worker never plots. The build
  measures the image and records it.
- **The licenses.** astrometry.net is BSD-3-Clause, with GPL-2+ and
  GPL-3+ parts (`qfits-an`, `util/ctmf.c`). SourceExtractor is
  GPL-3.0+. The image ships each program's license and copyright file
  through `notices/`, as the INDI images do. The recipe is MIT, like
  every component.
- **The smoke check** runs `source-extractor` on a small FITS image
  that the component commits, builds an `.axy` with the options above,
  and checks that `astrometry-engine --help` loads its libraries. No
  index file is committed: they are too large, and the drill solves.

### The index files of the lab

The lab's `Solver` claim holds 4207 and 4208 from the laptop's
`/usr/share/astrometry`, where Debian's downloader packages named them
by scale:

| On the laptop | Upstream name | Files | Bytes |
|---|---|---|---|
| `index-2mass-07-00.fits` to `index-2mass-07-11.fits` | `index-4207-00.fits` to `index-4207-11.fits` | 12 | 165,438,720 |
| `index-2mass-08.fits` | `index-4208.fits` | 1 | 81,835,200 |
| | | 13 | 247,273,920 (236 MiB) |

The engine reads each file's identity from its headers (`INDEXID`
4207, `HEALPIX` 5), so the names do not matter. The pool of claims on
`node-1` is 2 GiB, so the index files take 12% of it. Tycho-2 4108 alone,
78.8 MB, also solves this field, near M42, but 2MASS reaches fainter
stars, like the simulator's GSC with its limit of magnitude 17, so
2MASS is the lab's default.

### The `Solver` resource

```yaml
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Solver
metadata:
  name: lab
  namespace: observatory
spec:
  indexFiles:
    claimName: astrometry-indexes
    path: ""
status:
  endpoint: {service: lab-solver, host: lab-solver.observatory.svc, port: 8080}
  indexes:
    - {id: 4207, files: 12, scaleArcmin: {low: 22, high: 30}}
    - {id: 4208, files: 1, scaleArcmin: {low: 30, high: 42}}
  conditions:
    - {type: Ready, status: "True", reason: Loaded}
```

- **`indexFiles`** is a claim, and a directory in it, that holds index
  files. The solver reads every `index-*.fits` in the directory.
- **The operator runs** a `Deployment` of one pod, with the `Recreate`
  strategy, and a `Service`, in the `Solver`'s namespace. The pod
  mounts the claim read-only and runs the operator's own binary in its
  `solver` mode, in the image `astrophotography-operator-worker`,
  which builds `FROM astrometry` ([plan
  08](08-the-component-and-the-first-session.md)).
- **`status.indexes`** lists each index series in the claim with its
  scale, which the solver reads from each file's headers when it
  starts. The operator reads it once when the pod turns `Ready`. A
  person who adds index files restarts the `Deployment`, and the new
  pod reads them.
- **`Ready`** is `True` with the reason `Loaded` when the pod is ready
  and holds at least one index file. The `False` reasons are
  `ClaimMissing`, `NoIndexFiles`, and `PodNotReady`.
- **A target names its solver** in `spec.centering.solver`, in its own
  namespace. A `Solver` in another namespace is not reachable by name in
  this plan; nothing in the protocol prevents it later.
- **A download of index files is a later step.** The decisions let the
  operator fetch the files that the trains need from
  `data.astrometry.net`. The first session needs no download, because
  the lab fills the claim from the laptop, and the laptop's link is
  slow. A field `spec.download`, with the series and the scales, and a
  `Job` that writes each file under a temporary name and renames it,
  come after the first session.

### How a solve request reaches the engine

The `solver` mode is a small HTTP server that runs one
`astrometry-engine` process for each request, in one container:

1. `POST /solve` carries one `.axy` in the body, a few kilobytes. The
   server writes it as `/work/<id>.axy` in an `emptyDir`, where `<id>`
   is a new UUID, so its outputs are `<id>.wcs` and `<id>.solved`.
2. The server runs `astrometry-engine` with the index configuration
   and the path of the `.axy`, and waits for the process to exit. The
   exit is the end of the job, whether or not it solved.
3. The answer is the solution from `<id>.wcs`: the center from
   `CRVAL1` and `CRVAL2`, the scale and the rotation from the `CD`
   matrix, the parity, and the index that solved it, or "no solution"
   when the engine wrote no `.wcs`. The server then deletes the job's
   files.

The server runs one job at a time and holds a queue, so each request
waits its turn. A solve took about 1 s, and each reservation centers a
few times an hour, so one solver serves many telescopes. Each `.axy`
carries a CPU limit of 60 s (`ANCLIM`), so a field that cannot solve
does not hold the queue.

The design conversation chose one engine that keeps running and reads
paths on its standard input. It writes no file for a field it does not
solve, so the server could tell that a job ended only from the engine's
first open of a second file, through `inotify`, which the probe showed
works but which depends on the order in which the engine opens files.
A process for each request ends each job with its exit, and the probe
measured its cost at 0.06 s with the lab's 13 index files and 0.56 s
with all 205 of the laptop's. The solver stays central, one
`Deployment` for many telescopes, and only the process is new for each
solve. A resident engine can return if a large index set makes the
start cost matter.

### Extraction beside the frames

The operator runs one worker for each reservation it holds: a
`Deployment` of one pod and a `Service` named
`<Reservation>-astrophotography`, in the reservation's namespace, owned
by the `Reservation`. The pod mounts the outbox claim at `/outbox` and
runs the operator's binary in its `worker` mode, in the same image as
the solver. A `local-path` outbox is bound to the pier machine, and a
`ReadWriteOnce` claim lets several pods of one node mount it, so the
worker starts on the node of the camera. A claim with the access mode
`ReadWriteOncePod` cannot be shared, so the worker's pod stays
`Pending`, and the reservation gets the `Warning`
`AstrophotographyWorkerPending` with the scheduler's message.

The worker answers the operator alone:

- `POST /extract` names a frame in the outbox and the hints: the
  expected center, a search radius, and the range of the plate scale.
  The worker runs `source-extractor` with a catalog of `X_IMAGE`,
  `Y_IMAGE`, `FLUX_AUTO`, `FWHM_IMAGE`, `ELONGATION`, and `FLAGS`, then
  `solve-field --just-augment --no-remove-lines --uniformize 0
  --crpix-center` with the hints, and answers with the `.axy` and the
  star count and median `FWHM_IMAGE`. The catalog's other columns are
  there for the grading of [plan 06](06-grading-policies.md).
- `GET /outbox` answers the free bytes of the outbox's file system and
  the frames of each target's directory, for [plan
  01](01-run-a-targets-sequence.md) and [plan
  03](03-promotion-to-the-archive.md).
- `DELETE /centering/<FRAMEID>` removes a centering frame after its
  solve. The worker deletes nothing else.

The operator carries each `.axy` from the worker to the solver, so the
worker and the solver never talk to each other. The operator already
reaches the INDI server and PHD2 over the cluster's network, and a
`NetworkPolicy` that the cluster owner writes needs only one more pair
of paths: from the operator to the worker and to the solver, on port
8080. The manual names their labels.

### Centering

1. **The plate scale** is 206.265 × the camera's pixel size in µm ×
   the binning / the focal length in mm, from the camera's
   `status.info` and the session's tube. The hint is that scale ±10%.
2. **The first slew** goes to the target converted from J2000 to JNow:
   precession and nutation, from Meeus's *Astronomical Algorithms*,
   chapters 21 and 22, in a small package of this operator, `sky`,
   whose tests hold the book's worked examples. [Plan
   04](04-the-scheduler.md) chooses its source of the sky's geometry,
   and can replace the package.
3. **A centering exposure** of `centering.exposure` (default 5 s),
   through `centering.filter` (default the first sequence's filter),
   lands in `_centering`, with its own `FRAMEID`.
4. **The solve:** the worker extracts with a hint of the target and a
   radius of 5°, and the solver solves. With no solution, the operator
   tries once more with twice the exposure, and then sets `Imaging`
   `False` with the reason `CenteringFailed`.
5. **The error** is the angle between the solved center and the target,
   both J2000. Within `centering.tolerance` arcseconds (default 30),
   the target is centered.
6. **Otherwise, sync and slew again:** the operator sets `ON_COORD_SET`
   to `SYNC` and writes the solved center, converted to JNow, as the
   mount's position, and then slews to the target in JNow again. The
   same conversion is on both sides of the sync, so an error of the
   conversion cancels. After `centering.attempts` rounds (default 5)
   outside the tolerance, `Imaging` is `False` with the reason
   `CenteringFailed`.

A sync corrects where the mount thinks it points, so the next target's
slew lands closer too. An offset slew, which moves by the error and
leaves the mount's model alone, was set aside, because every later
slew would miss by the same amount. Ekos aligns the same way by
default.

`status.centering` records the last result: the reservation, the
time, the rounds, the solved center, the rotation, the scale, and the
error in arcseconds. The rotation is recorded and not corrected; a
framing angle with the `Rotator` can come later.

In the lab, the simulator draws the stars of the J2000 point that its
mount reports, so the first solve after a slew lands within the
difference between this operator's conversion and libnova's, and the
first sync removes it. The drill of [plan
08](08-the-component-and-the-first-session.md) checks that the error
falls within 30 arcseconds in at most two rounds.

## The image

Built on 2026-10-12 as `ghcr.io/liken-sh/astrometry:20260928-1`,
from the top-level component `astrometry/`, and pushed to the lab
registry. The build downloaded 117 Debian packages, 41.7 MiB, and the
image is 52,178,737 bytes (49.8 MiB), with 46 packages in its notices
and no Python. It is smaller than the 88 MB the laptop's closure
measured, because Debian's packages with no recommends install the
reference BLAS and LAPACK in place of OpenBLAS, and its `solve-field`
does not link `cairo`.

The smoke check, `astrometry/smoke/astrometry.sh`, solves a synthetic
field of 256 by 256 pixels against an index file of the same synthetic
stars, 104 KB, that the component commits, with the options of [the
worker](#extraction-beside-the-frames). It solved at 150.000002°,
30.000009°, and 2.2353 arcseconds per pixel, against the field's
150°, 30°, and 2.235. So 0.97 of Debian 13 reads `--just-augment
--no-remove-lines --uniformize 0` as 0.98 does, and runs no other
program.

The image also solved a frame of the CCD simulator with the lab's 13
index files of 4207 and 4208, each step in its own container on the
laptop. The frame, made with a focal length of 400 mm, is 1280 by 1024
at 2.68 arcseconds per pixel.

| Step | Wall time, with the container's start |
|---|---|
| `source-extractor`, 317 stars | 0.26 s |
| `solve-field --just-augment`, an `.axy` of 20,160 bytes | 0.25 s |
| `astrometry-engine`, solved with `index-2mass-07-00.fits` | 0.28 s |

The solution was 29.6070°, 59.8193°, at 2.6815 arcseconds per pixel,
with 39 stars matched. The frame's header gave `OBJCTRA` and `OBJCTDEC`
of 35.63°, 53.82°, which is 6.8° from the stars that the simulator
drew. With that point as the hint and a radius of 5°, the engine
searched for 58 s and wrote no solution. The laptop's 0.98 gave the
same result for the same `.axy`. With a radius of 10° the image solved
the frame as above.

## Still open

- The download of index files, and the choice of series from each
  train's field, after the first session.
- A `Solver` that serves targets of other namespaces.
- Why the header of the frame `C_001.fits` names a point 6.8° from the
  stars that the simulator drew, when the probe above found the two
  the same. The drill checks it on a frame of a reservation.
