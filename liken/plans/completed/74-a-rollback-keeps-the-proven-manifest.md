# 74. A rollback keeps the proven manifest

Milestone 74. Built 2026-10-08. It closes the open problem of a
rollback that drops a manifest with a newer field. It protects a
rollback to any release that carries it, which is every release later
than `2026.10.08-002`. A rollback to `2026.10.08-002` or earlier still
needs the steps in the rollback guide. The QEMU drill is in
[What the lab measured](#what-the-lab-measured).

## The problem

A person sets a `Machine` field that a newer release added, such as
`spec.serio`. The field applies, and the manifest that holds it
becomes the machine's proven manifest. When `spec.version` then
points the machine at an older release, the older `init` reads
`proven.yaml` with `machine.Parse`. That parse is strict, so a
misspelled field fails where a person sees it, and it cannot tell a
misspelling from a field a newer release added. The proven manifest
fails, and `init` loads the seed from the release's image on the
slot:

* When the seed does not name the field, the machine boots under the
  install-time manifest, whose storage and network can be stale, and
  `settleManifests` writes that seed over `proven.yaml`. If the seed's
  storage differs from the disk, the storage reconcile acts on it, and
  a created partition always formats.
* When the seed names the field too, the seed fails the same parse,
  and the machine powers off. Recovery needs an install stick.

The older operator had a second gap. It carried every condition in
`status.conditions` forward and never removed a type it did not know.
A newer condition that was `False` at the rollback, such as
`SerioAttached` with an adapter unplugged, stayed `False`. The older
phase table reads a reason it does not know as `Degraded`, and a
`Degraded` machine holds every other machine's reboot turn.

## The design

A fix protects only the releases that carry it, because a published
release keeps its strict parse. Two alternatives were weighed. A guard
in the newer operator could refuse to stage an older release while
the spec uses a field that release does not know, but it needs each
release to publish its schema, and the releases already published do
not. A lenient parse in the release that boots is the same reach for
less work, so this milestone builds that, and the conditions fix
beside it.

**The readers of a proved manifest skip fields they do not know.**
`machine.ParseKnown` runs the strict parse first. When that fails only
because of fields this release has no Go field for, it parses again
without them and answers the path of each field it skipped. A walk of
the decoded YAML beside the Go type finds the paths, and it matches
names without regard to case, as `encoding/json` does, so it agrees
with the strict parse. A known field with the wrong type, another
kind, or YAML that does not parse still fails.

Two programs read a manifest that a boot already proved, and both use
it. `init` reads `proven.yaml` through `provenCandidate`, and the
console names each skipped field. The machine operator reads the
manifest that `init` booted under, from `/run/liken/machine.yaml`,
through `machine.LoadKnown`. Without that second reader, the operator
would exit at startup on the same field. A staged manifest and a seed
still parse strictly, because a typo there must fail before anything
boots it.

**The operator keeps only the conditions it owns.**
`ownedConditionTypes` in `machine-operator/ownedconditions.go` lists
the types this release writes. Each pass starts from those, plus
`RebootApproved`, which the cluster operator writes, and drops every
other type. The list is explicit because a pass does not write every
condition on every pass: `VersionConverged` waits for a read of the
Cluster, and the Node's conditions wait for a read of the Node. A
pass that dropped what it did not write would drop those on a failed
read. A test runs a full pass and fails when the pass writes a type
the list does not name. It found `WirelessJoined` missing while this
milestone was built.

## Tests

`machine/known_test.go` covers a manifest this release knows, a new
top-level field, a new field inside a known object, and a new field
inside a list item, each with the paths it skips, and refuses a known
field with the wrong type, another kind, and YAML that does not parse.
It also reads a manifest file with `LoadKnown`, and pins the walk's
name matching: case aside, fields an embedded struct promotes, no
field tagged `-`, and a type that decodes its own JSON as one value.
`init/manifests_test.go` shows that a proven manifest with a newer
field stays a candidate with its own hash, and that a corrupted one is
no candidate. `machine-operator/ownedconditions_test.go` runs a full
pass over a status that holds a newer release's `False` condition and
the conductor's grant: the newer condition goes, and the grant stays.
With the filter removed, that test fails.

## What the lab measured

The drill ran on 2026-10-08 on the dev cluster's `node-1`, alone,
under OVMF, with `rebootPolicy: Manual`. Release `2026.10.08-904` came
from this tree. Release `2026.10.08-905` came from the same tree with
a temporary patch: a `spec.drillMarker` string, the schema revision
raised to 8, and a `DrillMarked` condition that the operator owned and
wrote `False` while the field was set.

On 905, the field was set, and a `dummy` module was added to
`spec.modules`. The module loaded live, and the live load promoted a
manifest that held both, so `proven.yaml` named `drillMarker`, as a
real field reaches it beside a change that applies. The field was then
removed from the spec, which 905 left in `proven.yaml` because no
change staged, and the `DrillMarked` condition was set to `True` by
hand, so the rollback took its turn with the stale condition in place.

`spec.version` then pointed at 904. The rollback boot logged `the
proven manifest names fields this release does not know, and this boot
leaves them out: spec.drillMarker` and booted under the proven
manifest on slot A. The boot record named the source `Proven`, and
`ModulesLoaded` was `True` with `dummy` in the kernel, so the machine
kept the newer manifest's other fields. The 904 operator started with
no restart, logged the same field, and dropped `DrillMarked`. Every
condition read `True`, and the phase was `Ready`.
