# 02, Solving and centering

Stub. Proposed on 2026-10-12. Not designed.

A mount's slew lands near a target, not on it, so the operator solves a
frame to find where the telescope points and nudges the mount until the
target is centered. Solving reads the frame's stars, and the frames
are in the outbox on the machine at the pier.

astrometry.net already splits the work at a small file. Extracting the
stars reads the whole frame and produces an `.axy` file, a list of star
positions with hints such as the field width, a few kilobytes. Solving
that list needs the index files and none of the pixels. So:

- A worker pod for each active reservation mounts the outbox and runs
  SourceExtractor on each frame it needs. The one catalog it writes
  feeds both the `.axy` and the star measurements that grading uses.
- A `Solver` that the person declares runs `astrometry-engine` once in
  the cluster, with a claim for the index files. Which index files it
  needs follows from the field width of each optical train it serves.
  A person can also fill the claim with index files they already have.
- A new pinned image component, `astrometry`, holds astrometry.net and
  SourceExtractor, the way `indi` holds INDI.

The software is BSD and GPL. The Tycho-2 index files are BSD, the 2MASS
index files are GPL, and the 2MASS catalog is public domain, so the
project can mirror them. ASTAP is a faster solver that a person could
choose instead, but it reads the whole image, and its star databases
state no license, so it is an option and not the default.

The mount reports JNow and the solver reports J2000, about 20
arcminutes apart for M42 in 2026, so centering converts between the
epochs.

To design: the `Solver` resource, how a solve request reaches it, and
how the index files are fetched. Not checked: whether
`astrometry-engine` keeps its index files loaded between inputs when
it reads them from standard input.
