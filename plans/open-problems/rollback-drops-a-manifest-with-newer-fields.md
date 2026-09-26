# A rollback drops a manifest that uses a newer field

Open problem, high priority for any machine that uses a field a
previous release does not know. When a machine rolls back to a release
whose `Machine` type lacks a field its manifest uses, the older init
cannot read its own last-known-good manifest.

## Failure path

`machine.Parse` in [machine.go](../../machine/machine.go) parses
strictly, so a misspelled field is an error that someone sees. An
older release has no way to tell a misspelling from a field a newer
release added.

A field that converges live reaches the proven manifest without a
reboot: the live load promotes the manifest that contains it. After
`spec.version` points the machine back at an older release, the older
init reads `proven.yaml` in `loadManifests`
([manifests.go](../../init/manifests.go)), fails the strict parse, and
logs that the proven manifest is unreadable. With no staged manifest,
it has no candidate, so it loads the seed from the deployment layer on
the slot. Two outcomes follow:

- **The seed is the install-time manifest.** The machine boots under
  it, and its storage and network may be stale. `settleManifests` then
  writes the seed over `proven.yaml`. If the seed's storage differs
  from the disk, the storage reconcile acts on it, and a created
  partition always formats.
- **The seed declares the newer field too.** `loadSeed` fails, and
  the machine powers off. Recovery needs a new install stick.

The older operator has a second gap: it carries every condition in
`status.conditions` forward, and it never removes a condition type it
does not know. A newer condition that was `False` at the rollback, such
as `SerioAttached` with an adapter unplugged, stays `False`. The older
phase logic reads its unknown reason as Degraded, and a Degraded
machine stalls the fleet's rollout turns.

Milestone 70's `spec.serio` is the first field known to take this path
in practice. Every earlier spec field has the same hazard, and nothing
guards against it.

## What exists today

The rollback guide and skill tell a person to remove a newer field and
let the removal stage before the rollback. A staged manifest without
the field is tried before the proven one, so the older init boots it.

## Directions

- **A guard before the rollback.** The operator refuses to stage an
  older release while the spec uses a field that release does not
  know, and reports Blocked with the field's name. The operator must
  learn each release's schema, for example from the catalog.
- **A lenient fallback parse.** Init keeps the strict parse for a
  staged manifest, and parses the proven manifest leniently only when
  the strict parse fails, reporting each field it ignored. This
  protects rollbacks to releases that carry it, and not to releases
  that are already out.
- **Conditions a release does not know.** The operator removes, or
  ignores for the phase, a condition type it does not own.
