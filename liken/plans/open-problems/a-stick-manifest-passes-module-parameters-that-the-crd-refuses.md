# A stick manifest passes module parameters that the CRD refuses

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

A `Machine` manifest on an install stick never passes admission, so
init filters `spec.moduleParameters` itself. The filter drops an empty
value and a key over 129 bytes. It passes a key that is not a module
name, a dot, and a parameter name, and a value with a space or a
double quote. The boot record also gets the unfiltered map, so one such
key fails the whole `boot/moduleParameters` write.

## The evidence

The `Machine` CRD (`machine/manifests/machines-crd.yaml:559` to `611`)
refuses a value that matches `\s` or `"`, a key that does not match
`^[A-Za-z0-9][A-Za-z0-9_-]*[.][A-Za-z0-9][A-Za-z0-9_-]*$`, and a key
over 129 characters.

`usableModuleParameters` (`init/moduleparams.go:37`) checks only an
empty value and `moduleParameterKeyMax`. `loadDeclaredModulesFrom`
(`init/modules.go:101`) uses its result, and
`machine.ModuleParameterString` (`machine/moduleparameters.go:31`,
called at `init/modules.go:191`) builds the
string for `finit_module` from it. That function skips a key with no
dot, but it joins the values with spaces and does not check them, so a
space in a value splits it into a second parameter for the module.

`init/main.go:231` copies `m.Spec.ModuleParameters` into
`boot.ModuleParameters` before any filter, and `publishBootFacts`
writes it with `WriteBootModuleParameters` (`init/facts.go:99`).
`writeKeyedScalars` (`machine/factswrite.go:311`) checks every key
with `assertKey` before it writes any, and a key outside `keyPattern`
(`machine/factstree.go:130`), such as one with a space or a slash,
returns an error for the whole directory. The comment on
`usableModuleParameters` says it exists so that the boot record does
not lose a parameter, but the boot record does not read its result.

One unit test called `usableModuleParameters` with each kind of
parameter that the CRD refuses and that the filter keeps: a value with a
space, a value with a double quote, and a key with no dot. It asserted
that each one is dropped, and each case failed. The first case failed
with:

```
usableModuleParameters kept snd_hda_intel.model="auto quirk=1", want it dropped as the CRD refuses it
```

A second test called `WriteBootModuleParameters` with
`snd_hda_intel.power_save` and a key with a space, `snd hda.model`. It
asserted that `snd_hda_intel.power_save` is recorded, and failed with:

```
boot/moduleParameters has no snd_hda_intel.power_save (stat /tmp/TestABadParameterKeyDoesNotHideTheOthersFromTheBootRecord2169324640/001/boot/moduleParameters/snd_hda_intel.power_save: no such file or directory), want it recorded beside a key the CRD refuses
```

The test is not in the tree, because a failing test cannot land on
`main`. The CRD also limits a value to 256
characters, and the filter does not check that limit either.

## What would confirm it

The unit tests above confirm it.

## What a fix has to decide

- Does init apply every rule that the `Machine` CRD has for
  `spec.moduleParameters`, and where does the one copy of those rules
  live, so that the CRD and init cannot disagree?
- Does the boot record hold the parameters that the manifest declared,
  or the parameters that init gave the kernel? `liveload.go` already
  computes the second with `deliveredParameters` for a live load.
