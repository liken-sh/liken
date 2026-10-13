# Working on astrophotography-operator

This directory holds the operator that will run imaging sessions on an
observatory that `observatory-operator` controls, and the manifests
that run it. `plans/00-design.md` is the design, and the `plans/`
directory holds the plans that build it. Code exists only where a plan
calls for it. This component depends on `observatory-operator`, and
`observatory-operator` does not depend on it. [Root plan
74](../plans/74-astrophotography.md) records the contract between the
two components, and [plan 08](plans/08-the-component-and-the-first-session.md)
gives the order in which the pieces of plans 01, 02, and 03 build.

The layout follows `observatory-operator`'s: the operator's
`package main` flat at the top, one domain to a file, and these
packages:

- `astrophotography/`, the Go types of `Target` and `Solver`, whose
  CRDs in `deploy/` are written by hand.
- `sky/`, the conversion between J2000 and the equinox of the date,
  from Meeus's *Astronomical Algorithms*.
- `outbox/`, the layout of a reservation's outbox, which the runner,
  the worker, and the promotion `Job` share.
- `catalog/`, the line of a target's frame catalog.
- `promote/`, `worker/`, and `solver/`, the three modes of the binary
  that do not run the operator. Each holds the contract that the
  operator speaks with it: the promotion report, and the HTTP requests
  and answers of the worker and the solver.

The operator imports `observatory-operator`'s `indi`, `phd2`, and
`observatory` packages through the `replace` in `go.mod`, and the
`Dockerfile` copies those three packages from the build context
`observatory`. A change to them runs this component's tests too.

`make test` runs every check CI runs. The manual at
https://liken.sh/astrophotography/ changes with the code, and
`docs/AGENTS.md` covers writing it. The resource pages are generated
from the CRDs, and `skills/` is generated from the guides, so run
`make -C docs generate skills` after an edit to either.

## The resources

The tests in `astrophotography/` read the CRDs through the API
server's own validators and hold them to the Go types, the way
`observatory-operator`'s tests do:

- `drift_test.go` compares each Go type with its CRD, field by field.
- `enum_test.go` holds each CRD enum equal to the Go constants.
- `validation_test.go` runs resources that a person could write
  through the structural schema and the CEL rules, and
  `target_test.go` checks the defaults that the CRD fills in.
- `fitskeywords_test.go` holds the `Target`'s `spec.fitsKeywords` to
  `observatory-operator`'s field, and its refused names to
  `RefusedFITSKeywords`.
- `example_test.go` holds `examples/first-session.yaml`, the drill of
  plan 08, to both operators' CRDs, to the Go types, and to itself.

## The operator

`main.go` builds the clients, and `leader.go` waits for the `Lease`,
runs the operator, and releases the `Lease` after the last write.
`operator.go` opens the watches of `watch.go`, which ring one bell on
every change: the `Target`s and `Solver`s, the `Reservation`s whose
`spec.holder` is `astrophotography-operator`, the kinds of
`observatory-operator` that the runner reads, and the pods,
`Service`s, `Deployment`s, and `Job`s the operator creates.
`metrics.go` holds the registry and the listener of root plan 65, and
`events.go` the reasons of the `Event`s on a `Reservation`. Each loop
of plans 01, 02, and 03 starts in `operator.run` and waits on the bell.

The tests run the operator in a `testing/synctest` bubble against
`fakeapi_test.go`, an API server on `kubernetes/apiservertest` that
holds every watched collection, and the Lease server of
`kubernetes/election/electiontest`.
