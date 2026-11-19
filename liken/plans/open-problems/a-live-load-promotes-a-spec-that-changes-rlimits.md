# A live load promotes a spec that changes rlimits

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Init's live module loader refuses a staged spec that changes storage,
the network, the parameters of a loaded module, or that retracts a
module or a serio entry. It does not compare `spec.rlimits`. So when
the staged spec also changes `rlimits`, init loads the added modules,
promotes the spec to proven, and writes the promoted hash to
`boot/manifest`, but the running k3s keeps the limits that it started
with. A spec then becomes proven without the boot that applies it.

## The evidence

`moduleLoader.apply` (`init/liveload.go:75`) calls `StorageDrift`,
`NetworkDrift`, `ModuleSetDiff`, `ModuleParameterDrift`, and
`SerioSetDiff` on the staged spec, and returns early when one of them
reports a change. No check calls `RlimitDrift`. After the checks it
calls `store.Promote` (`init/liveload.go:149`) and
`WriteBootManifest(machine.ManifestSourceProven, hash)`
(`init/liveload.go:177`).

machine-operator requests a load only when the drift holds the added
modules and serio entries and nothing else. `decideConvergence`
includes `RlimitDrift` in its drift (`machine-operator/converge.go:326`),
so a spec with an rlimit change takes the reboot path
(`machine-operator/converge.go:453`). The gap shows only when init
acts on an intent that does not match the staged spec. `apply`
handles that case on purpose: when the intent's `ManifestHash`
differs, it judges the staged copy (`init/liveload.go:92`), and the
comment on the loader says that a stale or forged intent "still
applies nothing that a reboot would apply differently". An rlimit
change is something a reboot applies differently.

One sequence that can produce it: the operator writes a modules
intent for a spec that adds a module. Before init reads the intent, a
person adds an rlimit to the spec, and the operator stages the new
spec for a reboot. Init then reads the old intent, judges the new
staged spec, and promotes it.

## What would confirm it

A test of `moduleLoader.apply` with a staged spec that adds a module
and changes `rlimits` against the boot record. If `apply` promotes the
spec and writes the proven hash, the bug is confirmed.

## What a fix has to decide

- Whether `apply` adds an `RlimitDrift` check, or whether it inverts
  the rule the way `decideConvergence` does, and refuses any drift
  except added modules and serio entries, so that a new reboot-class
  field is refused from the day it is added.
- What the operator does after such a promotion on a machine that is
  already affected, where `boot/manifest` names a proven spec whose
  rlimits did not apply.
