# Durable and ephemeral claims

Plan 43. A `Catalog` puts its two central stores, the catalog pod's
claim and the progress store's claim, on a durable class at sizes of
their own, while every `Library`'s working copies and every screen's
copies stay on a node-local class. This changes the fields on the
`Catalog` and the class three claims take. No pod changes.

## The problem

The `Catalog` has two class fields. `spec.storage.storageClassName`
classes the catalog pod's claim, the progress claim, and both claims
of every `Library`. `spec.screens.storageClassName` classes both
claims of every screen. Plan 32 split the screens off because a screen
is pinned to the machine that holds its display, so a node-local
class fits it and nothing else has to.

The other four claims are not alike. The catalog pod's claim is the
catalog of record: every other agent syncs from it, and a machine
that loses it loses the namespace's catalog until a scan rebuilds it.
The progress claim is the record of who watched what, which no scan
rebuilds. A `Library`'s scan claim and enrichment claim are working
copies: a `Job` fills one from the catalog of record and syncs its
rows back, so a lost one costs a sync and nothing more. A cluster
with a durable class wants the two records on it and the working
copies off it, and the progress store, which is small next to the
catalog, wants a size of its own.

## The contract

- **`spec.storage` is the catalog of record.** Its size and class
  reach the catalog pod's claim as before. Its size is also the size
  of every working copy and every screen's catalog claim, because
  every agent holds the whole catalog. Its class is the default for
  the two blocks below.
- **`spec.progress` is the progress store's claim.** `size` and
  `storageClassName` are both optional, and each defaults to the
  field of the same name under `spec.storage`.
- **`spec.libraries` is the working copies.** `storageClassName` is
  optional and defaults to `spec.storage.storageClassName`. It
  classes both of a `Library`'s claims. There is no size, because
  nothing asks for a working copy sized differently from the catalog
  it holds.
- **`spec.screens` is unchanged.**

  ```yaml
  spec:
    storage:
      size: 1Gi
      storageClassName: synology-iscsi-storage
    progress:
      size: 256Mi
      storageClassName: synology-iscsi-storage
    libraries:
      storageClassName: local-path
    screens:
      storageClassName: local-path
      artCache:
        size: 2Gi
  ```

- **Every new field defaults to today's behavior.** A `Catalog` that
  names neither block reads the same as before, so a standing
  `Catalog` changes nothing.
- **A claim is created once and left alone.** The rule every claim
  follows: a bound claim's spec is immutable, so a class or size
  change reaches new claims and not standing ones. To move a standing
  claim, delete it, and the next pass creates it on the new class at
  the new size. The two central claims are safe to move only while
  they hold nothing worth keeping, because a deleted claim's rows are
  gone.
- **The status is unchanged.** `status.storageSize` stays the
  catalog's size. Nothing reads a progress size back.

## What was set aside

- A per-claim tier, such as `tier: durable | local` on each block with
  the classes named once. A cluster's classes are its own vocabulary,
  and a class name on each block says the same thing with no
  indirection.
- Defaulting `spec.libraries` to the screens' class. The working
  copies and the screens are both node-local on a cluster that splits
  its classes, but the default has to keep a standing `Catalog`
  unchanged, and that is the catalog's class.

## The drill

To be run on `liken-1`, which has one class, and on the house, which
has two.
