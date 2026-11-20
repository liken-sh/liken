# Working on astrophotography-operator

`astrophotography-operator` is a Go Kubernetes operator. `README.md`
says what it is for and describes its kinds.

## How it runs

The operator is one `Deployment` in `liken-system`. Only the pod that
has the `Lease` acts. The operator watches these objects:

- the `Target`s and `Solver`s
- the `Reservation`s whose holder is `astrophotography-operator`
- the kinds of `observatory-operator` that it reads
- the objects that it creates

For each of its reservations that is `Ready`, the operator connects to
the telescope's INDI server as one more client. When the telescope has
a `Guider`, it also connects to PHD2's event server. Through those
connections, it runs the night.

The operator's other modes run from the image
`astrophotography-operator-worker`. That image is the same binary,
built on the `astrometry` image:

- A worker `Deployment` in the reservation's namespace mounts the
  outbox and finds the stars of a centering frame.
- A solver `Deployment` for each `Solver` mounts the index files and
  solves one star list at a time.
- A promotion `Job` moves a target's frames from the outbox to the
  archive and appends each one to the target's catalog.

Only the operator connects to the worker and the solver. It copies
each star list from the worker to the solver. So the star list
travels over the network between nodes, and the frame does not. The
operator writes each target's progress into the
target's status.

## Design

A frame in the archive does not change. So anything that the operator
writes into a frame is hard to change later: the FITS keywords, the
frame's identity, and the suffix that the operator can add to a file
name. The catalog is beside the frames, and a person can read it long
after the cluster is gone. So these rules apply to the catalog:

- A field keeps its name and meaning once the operator writes it.
- The operator never writes a line again.
- A new kind of record goes on a new line.

This component depends on `observatory-operator`.
`observatory-operator` does not depend on this one. This operator
imports `observatory-operator`'s Go packages, so a change to those
packages also runs this component's tests. It reads only what
`observatory-operator` publishes for a reservation's holder.
`observatory-operator/AGENTS.md` describes the contract between them.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.

## Finding things

- `deploy/` contains the CRDs of `Target` and `Solver`, which are
  written by hand. The tests in `astrophotography/` compare them with
  the Go types through the API server's own validators.
- `astrophotography/fitskeywords_test.go` compares the `Target`'s
  `spec.fitsKeywords` with `observatory-operator`'s field. It also
  compares the keyword names that the field rejects with
  `RefusedFITSKeywords`.
- `astrophotography/example_test.go` checks
  `examples/first-session.yaml` against both operators' CRDs, the Go
  types, and its own references.
- The manual's reference pages and `skills/` are generated from the
  CRDs and the guides. Run `make -C docs generate skills` after you
  edit either. `docs/AGENTS.md` covers the manual.
