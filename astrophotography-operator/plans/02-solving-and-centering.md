# 02, Solving and centering

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation; the `Solver` resource is not
designed yet.

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

## Still open

The `Solver` resource, how a solve request reaches it, and how the
index files are fetched. Not checked: whether `astrometry-engine`,
reading inputs from standard input, keeps its index files loaded
between them.
