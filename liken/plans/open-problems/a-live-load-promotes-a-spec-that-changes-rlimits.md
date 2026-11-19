# A live load promotes a spec that changes rlimits

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

Init's live module loader refuses a staged spec that changes storage,
the network, the parameters of a loaded module, or that retracts a
module or a serio entry. It does not compare `spec.rlimits`. So when
the staged spec also changes `rlimits`, init loads the added modules,
promotes the spec to proven, and writes the promoted hash to
`boot/manifest`, but the running k3s keeps the limits that it started
with. A spec then becomes proven without the boot that applies it.

## The evidence

`moduleLoader.apply` (`init/liveload.go:68`) calls `StorageDrift`,
`NetworkDrift`, `ModuleSetDiff`, `ModuleParameterDrift`, and
`SerioSetDiff` on the staged spec, and returns early when one of them
reports a change. No check calls `RlimitDrift`. After the checks it
calls `store.Promote` (`init/liveload.go:139`) and
`WriteBootManifest(machine.ManifestSourceProven, hash)`
(`init/liveload.go:164`).

machine-operator requests a load only when the drift holds the added
modules and serio entries and nothing else. `decideConvergence`
includes `RlimitDrift` in its drift (`machine-operator/converge.go:265`),
so a spec with an rlimit change takes the reboot path
(`machine-operator/converge.go:362`). The gap shows only when init
acts on an intent that does not match the staged spec. `apply`
handles that case on purpose: when the intent's `ManifestHash`
differs, it judges the staged copy (`init/liveload.go:85`), and the
comment at the top of `init/liveload.go` says that a stale,
duplicate, or forged intent "applies nothing that a reboot would
not". A live load does not apply an rlimit change, and a reboot does.

One sequence that can produce it: the operator writes a modules
intent for a spec that adds a module. Before init reads the intent, a
person adds an rlimit to the spec, and the operator stages the new
spec for a reboot. Init then reads the old intent, judges the new
staged spec, and promotes it.

A unit test staged a spec that adds a module and sets
`spec.rlimits` to `nofile: "1048576"`, against a boot record with no
rlimits, and called `moduleLoader.apply` with an intent whose hash
differs from the staged spec. It asserted that the spec stays staged
and that `boot/manifest` does not name it, and failed with both lines:

```
a manifest that changes spec.rlimits was promoted, want it staged until a boot
boot/manifest names the staged spec 67a1f5b215f1 as proven, want it unchanged
```

The test is not in the tree, because a failing test cannot land on
`main`.

The loader has no record of the rlimits that the machine booted with:
`moduleLoader` holds the boot's storage, network, modules, module
parameters, and serio entries, and no rlimits. So an `RlimitDrift`
check needs that value from `main` as well. After such a promotion,
`decideConvergence` finds the boot hash equal to the spec's hash with
rlimit drift left, and reports `BootMismatch`, so the operator does not
reboot the machine to apply the limits.

## What would confirm it

The unit test above confirms it.

## What a fix has to decide

- When the staged spec also changes `spec.rlimits`, does init's live
  load check rlimits by name, which needs the boot's rlimits passed
  to the loader, or does it refuse every change except added modules
  and serio entries, as `decideConvergence` does? The second form
  also refuses a field that needs a boot from the day the field is
  added.
- How does a machine that is already affected recover? Its
  `boot/manifest` names a proven spec whose rlimits did not apply, and
  the operator reports `BootMismatch` and does not reboot it.
