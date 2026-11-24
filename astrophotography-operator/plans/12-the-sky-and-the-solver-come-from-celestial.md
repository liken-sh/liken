# 12, The sky and the solver come from celestial

Proposed on 2026-11-24. Nothing is built.

## The problem

The operator's astronomy is code of its own, and the plans that are
not built yet need more of it than `sky/` has.

- **`sky/` is a truncated port of Meeus.** It converts J2000 to the
  equator and equinox of date with an abridged nutation series, and
  it finds alt-az and the next rise at one-minute steps. It works in
  UTC, with no TT or UT1, and it applies no aberration, refraction,
  or parallax. Centering syncs the mount after each solve, so the
  error of the conversion cancels there. Any other use of a position
  gets the error.
- **The plans need a body's position, not only a star's.** [Plan
  09](09-a-targets-position-by-name.md) resolves names, and a name
  can be `moon` or `jupiter`. [Plan
  10](10-planetary-and-solar-imaging.md) images the planets and the
  sun. [Plan
  11](11-the-scheduler-chooses-by-the-sun-the-moon-and-the-work-left.md)
  needs the sun's altitude, the moon's position and phase, and
  forecasts of rise and set. Plan 11 plans to port Meeus's chapters 25
  and 47, and it records that nobody checked the accuracy of the
  moon's truncated series.
- **The sun has a formula in each operator.** `observatory-operator`'s
  `dawn.go` uses the USNO low-precision formula, and `sky/` uses
  Meeus. They share no code.
- **A solver needs index files that a person chooses and copies.**
  astrometry.net's index files each cover one range of quad sizes. So
  the [solver guide](../docs/content/docs/guides/set-up-a-solver.md)
  has the reader work out the plate scale, choose a series, download
  its files, and copy them into a `PersistentVolumeClaim` by hand.
  The worker image is built on the `astrometry` image, whose solver
  and star extractor are Debian's packages, with GPL parts.

## Decisions

- **The operator's astronomy comes from
  [`celestial`](https://github.com/gaker/celestial)**, a set of Rust
  crates by Greg Aker, licensed MIT OR Apache-2.0. Each crate covers
  one domain:
  - `celestial-time`: the time scales, leap seconds, and sidereal
    time.
  - `celestial-coords`: the IAU 2006/2000A reduction from ICRS to
    observed positions, refraction, proper motion, and IERS Earth
    orientation data.
  - `celestial-ephemeris`: VSOP2013 for the sun and the planets, and
    ELP/MPP02 for the moon.
  - `celestial-wcs`: pixel to sky through TAN and SIP.
  - `celestial-images`: the FITS reader.
  - `celestial-catalog`: a memory-mapped HEALPix star catalog from
    Gaia DR3 and Hipparcos.
  - `celestial-solver`: a plate solver that matches quads of stars
    against that catalog.

  Its tests compare each result with ERFA's, and the repository's
  rule is that results match bit for bit. On 2026-11-24, at commit
  `deps: drop chrono, bump quick-xml, nalgebra, dirs, lz4_flex,
  rustyline` of `main`, `cargo test --workspace` passed 3,745 tests
  with 56 ignored, and `cargo clippy --workspace --all-targets` gave
  no warnings. Its book reports the ephemeris against DE432s over
  1950 to 2050: the sun within 2.3 milliarcseconds and the moon
  within 38.
- **The operator stays in Go, and a Rust component answers its
  questions about the sky.** The Go operator keeps the night: the
  reservations, INDI, PHD2, the conditions, and every decision. The
  Rust component computes the facts that those decisions use. The
  operator asks a question once for each slew, each frame, or each
  choice of the next target, so each question can be one request:
  - The position of date of an ICRS position at a time, for INDI's
    `EQUATORIAL_EOD_COORD`.
  - The altitude, azimuth, and next rise of a set of targets at a
    site, against the site's horizon profile.
  - A forecast of the night for plan 11: the darkness, the moon's
    position and phase, and the altitude of each target.

  The operator's Go tests answer these requests with a fake, the way
  `fakepeers_test.go` answers for the worker and the solver. When the
  component does not answer, the operator does not slew, and it holds
  the target with a condition.
- **How the operator reaches the component is decided when it is
  built.** The Go images build with `CGO_ENABLED=0`, so the crates
  cannot link into the operator. The first choice to explore is a
  native sidecar container in the operator's pod, with a Unix socket
  in a shared `emptyDir`, because the kubelet then restarts it and the
  process keeps its tables loaded. A subprocess for each request, the
  way `solver/engine.go` runs `astrometry-engine`, and WebAssembly in
  the Go process through wazero are the others worth a measurement.
- **The worker and the solver become Rust programs.** The operator
  reaches them over HTTP, and they do not use INDI or PHD2. A Rust
  worker extracts the stars with `celestial-solver`, and a Rust
  solver matches them against the catalog. The operator passes the
  star list from the worker to the solver as bytes that it does not
  read, and it reads the solver's `SolveResponse`. So the Rust
  programs keep the requests and responses of `worker/api.go` and
  `solver/api.go`, and the format of the star list is theirs to
  choose. The Go operator changes where it builds the solver's
  `Deployment`, which mounts the catalog in place of the index claim.
  The promotion `Job` stays a mode of the Go binary.
- **A `Solver` names its catalog as an OCI artifact.** The solver's
  pod mounts the catalog as a Kubernetes image volume, by a reference
  with a digest, so a `Solver` names one artifact and nothing else.
  Each node pulls the artifact once and keeps it in containerd's
  store. The catalog comes in tiers by magnitude: a small tier for a
  wide field, and the tier to magnitude 18.5 for a long focal length.
  `celestial`'s prebuilt catalog to magnitude 18.5 is 4.5 GB, and it
  is on Google Drive, where it has no stable URL and no digest. The
  artifact also has the attribution that Gaia DR3's CC-BY-4.0 license
  asks for, the way `notices/` has each license.
- **`observatory-operator`'s `dawn.go` keeps its own sun formula.**
  It is the operator's only astronomy, and at the -18° crossing the USNO formula's error of
  about 0.01° moves the end of the night by seconds. It would get
  nothing from the crates.
- **Plans 09, 10, and 11 take their positions from the crates.** The
  sun, the moon, the planets, and the moon's distance from a target
  come from `celestial-ephemeris` and `celestial-coords`, and not
  from a port of Meeus. Plan 09 still needs OpenNGC for names, because
  `celestial` has no NGC, IC, or Messier catalog.

## Work with upstream

One person writes `celestial`, and the work of this plan has to stay
pleasant for its author. Each gap that this plan finds goes upstream as a
small pull request with ERFA-exact tests that follows the
repository's `CLAUDE.md`. We open one pull request at a time, and we
open no issue that asks the author to build something. When we need a fix
before it merges, the code stays in this repository until it does,
so the work here never waits on a review there.

The gaps that the plan needs filled, in the order the work needs
them:

1. **Pinnable releases.** On 2026-11-24 the newest release on
   crates.io was `0.1.1-alpha.2` from 2026-02-19, and
   `celestial-solver` was not published. The IERS data in
   `celestial-eop-data` 0.1.12 is from 2026-05-15, and
   `data_timestamp()` returns the time of the build, not the age of
   the data. A release workflow and a scheduled refresh of the Earth
   orientation data would fix both.
2. **The catalog as an OCI artifact, in tiers**, with a digest.
3. **The equator and equinox of date.** INDI's
   `EQUATORIAL_EOD_COORD` takes a position of date, and `celestial`
   gives CIRS. The conversion is the equation of the origins, ERFA's
   `eo06a`.
4. **A test of the whole chain from ICRS to observed az/el** against
   ERFA's `atco13`. As far as the survey read, `cirs_to_hour_angle`
   applies neither polar motion at the observer nor diurnal
   aberration. This test shows whether that is true.
5. **The apparent place of a body**, with light time, from the
   ephemeris to the coordinates. Today `celestial-coords` does not
   use `celestial-ephemeris`, and the ephemeris gives geometric
   vectors.
6. **Rise, set, transit, twilight, and the moon's phase and
   illumination.**
7. **A model of Delta-T** for a time past the Earth orientation data.
   Today the caller supplies it.

## Set aside

- **Port more of Meeus.** Plans 09 to 11 would add the sun, the moon,
  and the planets to `sky/`, with no reference to test them against.
  Each operator would keep its own sun formula.
- **`astrophotography-operator` in Rust.** The operator would call
  the crates directly, but it would also need Rust versions of the
  `indi` and `phd2` clients, their fakes, the API types of
  `observatory-operator`, and the guards of every watch. That
  rewrites and designs again the mechanics of an operator that works,
  to get positions that a few requests give.
- **The sky component as its own `Deployment` and `Service`.** It
  adds a network hop and a `NetworkPolicy` for answers that a process
  in the operator's pod can give. The solver is a `Deployment`
  because it mounts a large catalog and solves on any node.
- **Rust through cgo.** No component uses cgo, and the operator's
  static Go build would end.
- **Keep astrometry.net's index files.** A person would still choose a
  series by plate scale and copy its files into a claim.

## Proof

The Rust tests follow the rule of the Go tests: no wall clock, no
network, and no container. The join between the operator and the sky
component is proved on `liken-1` by hand.

- **Image volumes on liken.** On 2026-11-24, a pod on `liken-1`, at
  k3s `v1.37.2+k3s1` with containerd `2.3.7-k3s1`, mounted
  `quay.io/crio/artifact:v2` as an image volume and read its files.
  The `ImageVolume` feature gate is beta and on by default from
  Kubernetes 1.35. A drill still has to mount the catalog itself: a
  layer of several gigabytes, memory-mapped by the solver.
- **The solver against astrometry.net.** Before the `astrometry` image
  is retired, both solvers solve the same set of centering frames from
  the simulators and from `liken-1`. The comparison reports the share
  of frames that each solves, the time of each solve, and the
  distance between the centres they find. A blind solve, with no
  position hint, is in the comparison, because `celestial-solver`
  builds its quads from a cone of the catalog at solve time.
- **Positions against ERFA**, through the upstream tests of the list
  above.

## Still open

- The transport to the sky component, after a measurement of each
  choice in the decision above, and how the contract is versioned
  when the operator's image and the component's image differ.
- Whether `ci pins` accepts a git revision of `celestial` in
  `Cargo.lock`, until upstream publishes releases. `ci/deps.go`
  refuses a git source only for a crate of `liken-sh`.
- Where the catalog artifact is published, and who builds it. A build
  from Gaia's files downloads about 700 GB.
- The tiers, their magnitude limits, and their sizes.
