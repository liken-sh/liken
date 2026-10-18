# 81, A library that only reads

This plan, written on 2026-10-18, proposes a `Library` setting that
makes `library-operator` read a volume and never write to it. It is a
stub: it states the problem and the shape of the contract, and leaves
the open questions for the design.

## The problem

Two clusters can mount the same media volume. A test cluster often
mounts a production cluster's NFS exports, so that its screens show a
real library. Each `Library` over such a volume writes into the same
title folders as the production `Library`. It writes `.nfo` files, art,
trailers, trickplay sheets, appearances records, and the `.liken/`
ledgers. Each cluster then reads the other's ledger attempts and
overwrites them. A development build on the test cluster can write a
ledger format that the production release does not read. Both clusters
also spend the same provider allowances.

A `Library` can drop most writers today: `sources: []` stops every fact
that asks a provider, and `trickplay`, `appearances`, and `trailers`
are off by default. Three writers remain, and the spec cannot turn them
off:

* The probe phase writes `.liken/probe.yaml` on every walk.
* The arrival phase writes `.liken/arrival.yaml` on every walk.
* The close container of a walk `Job` removes the partial files that a
  stopped writer left behind.

A franchises `Library` also writes the art that its walk fetches onto
its art claim.

## The contract

A new field, such as `spec.readOnly: true`, makes the `Library` a pure
reader:

* Every container of every `Job` of the `Library` mounts the volume
  read-only, so a defect in a phase cannot write. The walk already
  mounts it read-only.
* The operator runs the walk and no phase that writes: no probe, no
  arrival, no fact, no worker, and no removal of partial files.
* The catalog reads what another writer left on the volume: the
  `.liken/` ledgers, the `.nfo` files, and the art. A title with no
  probe record has no length or codecs in the catalog.
* A webhook rescan of one folder still runs, because it is a walk.
* A `spec.refresh` key for a fact does nothing while the `Library` is
  read-only, and the status says so.

## Open questions

* Is the setting a top-level `spec.readOnly`, or `spec.storage.readOnly`
  beside the claim it describes?
* Does the API server refuse a read-only `Library` that also turns on
  `trickplay`, `appearances`, `trailers`, or names `sources`, through a
  CEL rule? Or does the operator accept it and report a condition?
* What does the catalog hold for a file that no other writer has probed
  yet? The walk could read the length from the file's container header
  without writing a record, or leave it empty.
* `arrival.yaml` sets the time that a title was first seen, and the
  "recently added" row sorts by it. Without it, the walk could use the
  file's modification time.
* Does a franchises `Library` in this mode read its art from the art
  claim and fetch nothing?

## Proof

A test runs a read-only `Library`'s walk `Job` against a volume and
checks that no file on the volume changed. A drill on `liken-1` turns
the mode on for its `movies` and `series` libraries and checks that a
walk changes no file under a title folder.
