# A stick manifest passes module parameters that the CRD refuses

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

A `Machine` manifest on an install stick never passes admission, so
init filters `spec.moduleParameters` itself. The filter drops an empty
value and a key over 129 bytes. It passes a key that is not a module
name, a dot, and a parameter name, and a value with a space or a
double quote. The boot record also gets the unfiltered map, so one such
key fails the whole `boot/moduleParameters` write.

## The evidence

The `Machine` CRD (`machine/manifests/machines-crd.yaml:628` to `653`)
refuses a value that matches `\s` or `"`, a key that does not match
`^[A-Za-z0-9][A-Za-z0-9_-]*[.][A-Za-z0-9][A-Za-z0-9_-]*$`, and a key
over 129 characters.

`usableModuleParameters` (`init/moduleparams.go:38`) checks only an
empty value and `moduleParameterKeyMax`. `loadDeclaredModulesFrom`
(`init/modules.go:101`) uses its result, and
`machine.ModuleParameterString` (`init/modules.go:191`) builds the
string for `finit_module` from it. That function skips a key with no
dot, but it joins the values with spaces and does not check them, so a
space in a value splits it into a second parameter for the module.

`init/main.go:231` copies `m.Spec.ModuleParameters` into
`boot.ModuleParameters` before any filter, and `publishBootFacts`
writes it with `WriteBootModuleParameters` (`init/facts.go:98`).
`writeKeyedScalars` (`machine/factswrite.go:326`) checks every key
with `assertKey` before it writes any, and a key outside `keyPattern`
(`machine/factstree.go:141`), such as one with a space or a slash,
returns an error for the whole directory. The comment on
`usableModuleParameters` says it exists so that the boot record does
not lose a parameter, but the boot record does not read its result.

## What would confirm it

A unit test of `usableModuleParameters` with a value that holds a
space and a key with no dot. A second test that runs the boot's
facts write with a key that holds a space, and checks
`boot/moduleParameters`.

## What a fix has to decide

Whether init applies the CRD's rules in full, and where those rules
live so that the CRD and init do not disagree. Also whether the boot
record holds the declared map or the delivered map, which `liveload.go`
already computes with `deliveredParameters` for a live load.
