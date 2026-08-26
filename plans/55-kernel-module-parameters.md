# Kernel module parameters

A Machine declares the parameters a module loads with, init passes
them to `finit_module`, and the status reports what the kernel holds.

## The problem

A module's parameters are set once, when the module loads. On a
distribution the setting comes from `/etc/modprobe.d/*.conf`, which
`modprobe` reads before it calls the kernel. liken ships no `modprobe`,
no `modprobe.d`, and no udev, and init calls `finit_module` with an
empty parameter string (`init/modules.go`). So a machine that runs
liken has no way to say how a driver loads. It can say only that the
driver loads.

Three cases came from `liken-1`, an Intel N95 that drives a monitor's
speakers and its display:

* `snd_hda_intel.power_save`. The vendored kernel build sets
  `CONFIG_SND_HDA_POWER_SAVE_DEFAULT=1`, so the HDA codec suspends
  after one second of idle. A machine with a sound server hides this,
  because the server holds the device open. A machine that plays
  through a pod does not, and every clip starts with a click and a
  dropped first syllable. The correct value for this machine is
  `power_save=0`, at load.
* `i915.enable_guc`. Which firmware paths the graphics driver uses is
  a load-time choice on this hardware.
* `drm.debug`. The bitmask that turns on KMS diagnosis, which is worth
  having for one boot and never after.

The workaround today is a pod with a `hostPath` on
`/sys/module/<name>/parameters`. It reaches only the parameters that
the module marks writable, it applies after the driver already
probed, it leaves no record in any document, and it must run again
after every boot. The declaration belongs in the Machine, beside the
module it modifies.

The third case is the one this design does not cover. The same
kernel build sets `CONFIG_DRM=y`, so `drm` is not a module here and
its parameters come from somewhere else. See "Not in this milestone".

## The declaration

The declaration is `spec.moduleParameters`, a map from
`<module>.<parameter>` to the value, beside `spec.modules`:

```yaml
spec:
  modules: [snd_hda_intel, i915]
  moduleParameters:
    snd_hda_intel.power_save: "0"
    i915.enable_guc: "3"
```

The key is the kernel's own spelling of a module parameter, the same
form that the kernel command line uses and that
`/sys/module/<name>/parameters/<parameter>` mirrors. The map is flat
for the same reason `spec.sysctls` is flat: a dotted key that the
kernel already defines needs no structure invented around it, and a
person who knows the parameter can find the line by its name.

The obvious alternative was to let each `spec.modules` entry be either
a plain name or an object with a name and parameters. It cannot be
built. A v1 CustomResourceDefinition must have a structural schema,
and a structural schema requires a `type` on every node except a node
with `x-kubernetes-int-or-string` or `x-kubernetes-preserve-unknown-fields`.
`x-kubernetes-int-or-string` is the only union Kubernetes defines, and
it does not cover string-or-object. Preserving unknown fields on the
items node would buy the union at the cost of every check on the
entry. It would also lose `x-kubernetes-list-type: set`, which is what
makes the API server refuse a duplicate module today.

Converting `spec.modules` to a list of objects would express it, but
the cost is too high. Every existing manifest holds
strings, in each deployment's git, in each image's deployment layer,
and on each install stick. The CRD serves one version, `v1alpha1`,
and declares no conversion strategy, so the change is a break with no
migration path. The status side is worse: the CRD ships in the image
and applies when a leader boots, so a fleet mid-rollout has machines
on the old release writing the old shape. A type change under
`status.boot.modules` would make those writes fail validation, and a
machine that cannot publish status is a machine the conductor stops
granting turns to. Both new fields are additive for this reason, on
the spec side and on the status side.

## What the loader does

`loadModule` walks a module's `modules.dep` entry from the last file
to the first, because depmod orders dependencies to load
right-to-left. The first field of the entry is the module's own file,
so the module itself is the last load in its chain. The parameter
string goes to that one file, and every dependency loads with an empty
string, which is what `modprobe` does.

Init builds the string for one module by taking every
`spec.moduleParameters` key whose module half names it, sorting by
parameter name, and joining `parameter=value` pairs with a space. The
sort makes the string stable, so the same spec produces the same
record on every boot and the drift comparison stays a string
comparison.

Three loads cannot take parameters, and the loader detects each one
before it asks the kernel for anything:

* **The module is built into the kernel.** `modules.builtin` already
  names these, and the outcome is already `Builtin`. A builtin takes
  its parameters from the kernel command line, which this field does
  not reach.
* **The module is already resident.** `/sys/module/<name>` exists
  before the declared pass runs, because the fixed list
  (`modules.conf`), a feature's list, or an earlier declared module's
  dependency chain loaded it. `finit_module` returns `EEXIST` for a
  resident module and ignores the parameter string, so the loader must
  read the residency before the load to report it.
* **The module did not load.** `Missing` and `Failed` have their own
  message, and a parameter for a module that is not in the kernel is
  not a separate problem.

The declared pass keeps loading in the order the manifest lists,
which is what makes the soft-dependency recipe work: a report that
says "declare `realtek`, then `r8169`" depends on that order. Loading
parameterized modules first would deliver more parameter strings and
break that recipe, so the loader keeps the order and reports the case
instead.

The console line gains the string it passed:

```
liken: modules: snd_hda_intel: loaded (power_save=0)
```

## How a change applies

A loaded module never reads its parameters again. So a parameter
change on a module that this boot already loaded is a reboot-class
change, and it flows through the machinery every other reboot-class
change flows through: `ModulesDrift` writes a line for it,
`gateDisruption` applies `rebootPolicy`, the conductor grants the
turn, and the next boot loads the module with the new string.

The live-load path stays exactly as it is. `converge.go` classifies
a spec change as live-applicable by counting: `len(drift) == len(added)`
with nothing retracted, which holds only when added modules are the
whole of the drift. That test is structural on purpose, so a new spec
field is reboot-class from the day it lands. Parameters keep the
property with one rule about where their drift lines come from:
`ModulesDrift` writes a parameter line only for a module that is in
both the desired and the actuated sets. A parameter that arrives with
a module the boot never loaded is part of that module's own added
line, and the live loader passes it at the load, because a module that
is loading for the first time takes its parameters normally. A
parameter on a module the boot already loaded writes its own line, the
counts no longer match, and the change takes the reboot path. Init
re-derives the same judgment from the same shared functions
(`init/liveload.go`), so a stale or forged intent still applies
nothing that a reboot would have to apply.

One rule keeps the live path honest about delivery. A live load
loads its added modules in the manifest's order, the same order a
boot uses, because the order decides which module arrives early as a
dependency and so cannot take its parameters. When an added module
is already resident anyway, the load applies the rest of the edit
and records in `boot/moduleParameters` only the keys it delivered.
The undelivered key then drifts, parameter drift on a loaded module
is reboot-class, and `rebootPolicy` takes the normal turn; the
reboot delivers it. The load has already promoted the manifest by
then, so this drift arrives under a manifest hash that matches the
boot record, the one state the operator's contradiction guard would
otherwise call a bug. The guard admits exactly that shape, parameter
lines alone on an unchanged module set, and every other drift under
a matching hash still holds as a contradiction
(machine-operator/converge.go). If the module is resident at boot too, from the
fixed list or the manifest's own order, the boot records the request
and the `ModuleParametersApplied` condition carries the story, so
the worst case is one reboot that ends Degraded with a message, and
never a loop.

One gap stays open, recorded here on purpose: reordering
`spec.modules` alone produces no drift, because the comparison is
set-based. An operator who moves a dependency earlier to route a
parameter to it gets the fix at the next reboot that happens for any
other reason, not a scheduled one.

Reconciling the writable parameters live, by writing
`/sys/module/<name>/parameters/<parameter>`, is deliberately not part
of this. Whether a parameter is writable is a per-parameter mode bit
with no rule behind it, so half a machine's declarations would
converge in a second and half would wait for a reboot, and reading
the spec would not tell a person which half. Worse, a successful
write does not mean the driver acts on the new value. Many parameters
are read once, when the driver probes the device. The machine would
report the value as applied while the hardware ran on the old one, and
a status that reports an actuation it did not get is worse than no
status. One rule holds instead: a parameter takes effect at the load,
and the load is the next boot.

## Validation

Admission checks shape, and one relationship:

* Each key matches `^[A-Za-z0-9][A-Za-z0-9_-]*[.][A-Za-z0-9][A-Za-z0-9_-]*$`,
  checked by a CEL rule on the map, the way `spec.rlimits` checks its
  resource names.
* Each value matches `^[^\s"]*$`. The parameter string is
  space-separated and the kernel's own parser treats a double quote as
  a grouping character, so liken refuses both rather than inventing a
  quoting rule. A comma-separated array value passes, which is the
  form the kernel uses for array parameters.
* The module half names a module that `spec.modules` declares, checked
  by a CEL rule on `spec`, where both fields are in scope:
  `self.moduleParameters.all(k, self.modules.exists(m, k.startsWith(m + '.')))`.
  The comparison is exact, so a key must spell the module the way
  `spec.modules` spells it. Module names take `-` and `_`
  interchangeably, and admission has no normalizer, so the rule states
  the spelling requirement and the message names it.
* `maxProperties: 64`, matching the module list's own cap.

Admission cannot check anything else, and each layer checks only what
it can read. It has no way to check that the module exists in this
kernel build, that the parameter exists on that module, that the value
is in range, or that the module is built in. The build cannot check
either. Since milestone 32 the image holds the kernel build's whole
module tree, so the build no longer resolves declared module names at all,
and parameters get the same treatment.

The load is where the answer comes from, and the kernel gives two
different ones:

* **An unrecognized parameter name loads the module anyway.** The
  kernel's `unknown_module_param_cb` prints `<module>: unknown
  parameter '<name>' ignored` and returns success. `finit_module`
  returns no error, the module works, and the setting silently does
  nothing. The warning reaches `/dev/kmsg`, which liken already
  relays as a pod's stdout (milestone 15), and nothing else in the API
  would show it. This case is the reason for the readback below.
* **A recognized parameter with a value the module refuses fails the
  whole load.** The setter returns `EINVAL`, `finit_module` returns
  `EINVAL`, and the driver is not in the kernel. The outcome is the
  existing `Failed` state, its message holds the errno and the
  string that was passed, and, as with every other module outcome,
  nothing here stops the boot.

## What the status reports

Two additions, each matching a field that already exists:

* `status.boot.moduleParameters`, a map, records what the winning
  manifest declared and this boot passed. This is the drift reference,
  the same way `status.boot.rlimits` is for limits and
  `status.boot.modules` is for the module list. It records the
  request, not the result.
* `status.modules[].parameters`, a map, records what the machine read
  back from `/sys/module/<name>/parameters/<parameter>` for the
  declared keys, verbatim. A key whose file cannot be read is absent.
  Absence usually means the parameter name is wrong, because the
  kernel accepts the module and ignores the name; it can also mean
  the parameter is real but unreadable, since a driver may register
  a parameter with no sysfs file at all (`libata.force` does) or
  with a write-only mode. The kernel's log line at the load is what
  separates the wrong name from the unreadable one. This follows
  `status.rlimits`, which reports what the kernel holds and omits
  what it could not set.

The two are not compared. The kernel prints a bool parameter back as
`Y` or `N`, an array with its own separators, and a charp as whatever
the driver stored, so a machine comparison of the declared string
against the readback would report false drift on the most common
parameter type there is. A person compares two fields that are next to
each other. Init compares nothing.

One new condition, `ModuleParametersApplied`, reports only the cases
liken can check without comparing values: a parameter declared for a
`Builtin` module, and a parameter declared for a module that was
already resident when the declared pass reached it. Both are
structural facts. The message names the fix, as every other outcome
message does: the command line for a builtin, and the fixed list or
the earlier declared module for a resident one. A False condition
has a new reason, and `conditionPhase` maps an unknown reason to
`Degraded`, which is the right phase here and needs no
entry in the phase table. A declared parameter that never reached the
kernel stays wrong until somebody edits the declaration, and Degraded
is the phase that says so.

`ModulesLoaded` keeps its present meaning and does not widen. A module
that loaded is loaded, whatever happened to its parameters.

## What was considered and set aside

* **A `modprobe.d` file in the image.** It would be a file that only
  liken's own loader reads, holding a syntax liken would have to
  implement, describing state that the Machine document should hold.
  The Machine is the whole truth about what a machine runs.
* **A nested map, `moduleParameters: {snd_hda_intel: {power_save: "0"}}`.**
  It groups by module, which reads well, and it costs a second level
  of `additionalProperties` and a key vocabulary that matches neither
  the kernel command line nor `/sys`. The flat dotted key already is
  the kernel's name for the thing.
* **Parameters for the fixed list and for feature modules.** Those
  lists ship with the release, and a machine that needs to modify how
  the OS loads its own drivers has a bug for liken to fix, not a knob
  to turn.

## Not in this milestone

**Parameters for built-in code.** `CONFIG_DRM=y` in the vendored
kernel build, so `drm.debug` is not a module parameter on this kernel
at all. Built-in code takes its parameters from the kernel command
line, and liken's command line is boot-entry state, not spec state.
Reaching it means solving a different set of problems. A UEFI machine
rewrites its boot entries on every boot (`init/bootentries.go`), so a
change would land on the boot after the one that wrote it, while a
BIOS machine's `grub.cfg` renders once at install time and never
again, so a change would reach nothing without a re-render. The
argument list is what the proving and fallback machinery uses, so a
bad value is a machine that does not come back. liken owns
`rdinit`, `root`, `console`, `panic`, and every `liken.*` argument, so
a `spec.kernelParameters` field would need a refusal list before it
had a feature. That is a milestone, not a section. Until it exists,
`status.boot.commandLine` reports what the machine did boot with, and
a builtin's writable parameters stay reachable the way `drm.debug`
is reachable today, by writing `/sys/module/drm/parameters/debug`.

**Unloading a module.** The kernel build sets `CONFIG_MODULE_UNLOAD=y`,
so this is a policy, not a limit. Loading stays one-way for the reason
milestone 18 gave: nothing on the machine can prove that no workload
is using a driver. A retracted module still needs a reboot, and so
does a parameter on a resident one, which is the same reboot.

## What a drill must show

Every path runs on the dev fleet, on a guest whose declared module has
parameters worth reading back:

* A parameter that takes: declare it, reboot, and read
  `status.boot.moduleParameters` against
  `status.modules[].parameters`.
* A parameter name that is wrong: the module loads, the condition
  stays True, the readback map lacks the key, and the machine-logs
  pod shows the kernel's `unknown parameter ... ignored` line.
* A value the module refuses: `Failed`, an `EINVAL` message naming the
  string that was passed, `ModulesLoaded` False, and a boot that
  finishes.
* A parameter added to a module the boot already loaded: one drift
  line, no live load, a staged manifest, a conductor turn, and the new
  string in the boot record afterward.
* A module added with its parameters in one edit: a live load with no
  reboot, and the readback showing the parameters took.
* A parameter declared for `drm`, which is builtin here, and one
  declared for a module the fixed list already loaded:
  `ModuleParametersApplied` False, phase `Degraded`, and two messages
  that name two different fixes.
* The admission refusals: a key with no dot, a key naming a module
  that `spec.modules` does not declare, a key spelled with `-` where
  the module list spells `_`, and a value with a space in it.

The field case is the acceptance test. `liken-1` declares
`snd_hda_intel.power_save: "0"`, and a clip played from a pod starts
with its first syllable intact, with no hostPath pod anywhere in the
cluster.

## The manual

`docs/content/docs/reference/machine.md` regenerates from the schema,
so the schema's descriptions are the text. Write them knowing that
they become the page, and state in `spec.modules` that its parameters
are in `spec.moduleParameters` and that they apply at the load.

The troubleshooting guide gains one entry: a parameter that reads
back missing means the parameter name is wrong, and the kernel said
so once, in the machine's log stream, at the moment of the load.

## What the lab measured

The QEMU drills ran on 2026-08-26 against a dev build. A declared
parameter took at the load (`nbd.nbds_max=5`) and read back
identically. A wrong name loaded the module, left the readback key
absent, and put the kernel's `unknown parameter` line in the
machine's log stream. A refused value (`nbds_max=abc`) failed the
module with `EINVAL` and the passed string in the message, and the
boot still settled its pods in 28.8 seconds. A parameter change on a
loaded module produced one drift line and went straight to
`AwaitingTurn` with no live load. A module added with its parameters
applied in place with no reboot. All six admission refusals fired
against the live API server with their messages. A parameter for the
builtin `loop` reported `ParametersNotApplied` naming the command
line, with `4` declared beside the kernel's `8`.

The resident case ran end to end on a machine with `rebootPolicy:
Auto`: the live load applied, the console said the string did not
reach the resident module, the undelivered key drifted under the
promoted manifest's own hash, the operator staged and took the
conductor's turn, and the machine rebooted itself and delivered the
parameter, 61 seconds from patch to reboot. The first attempt at
this drill found the operator's contradiction guard calling that
state a bug and holding the machine Blocked; the guard now admits
parameter-only drift on an unchanged module set, and nothing else.

Two readback limits the drills confirmed: a parameter registered
with mode 0 (`dummy.numdummies`) has no sysfs file, so the console
line is the only proof of delivery; and the kernel-log line for a
wrong name is only reachable because the log relay now survives a
reboot, which the same drills proved by reading the new boot's
records from sequence zero.
