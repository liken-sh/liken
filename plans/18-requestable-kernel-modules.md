# Requestable kernel modules

Milestone 18 — Done

The vendored kernel is Ubuntu's generic build, so a module already
exists in the package we fetch for nearly every driver that Linux
supports. modules.conf is the deliberately fixed, reviewable list of
the few modules that the image ships and init loads: the OS's own
needs, with each entry annotated with its reason. A machine whose
workloads use hardware (a GPU for transcoding, a USB accelerator, a
capture card) needs its drivers too. Today, the only way to get them
is to edit the OS. This milestone adds the general mechanism: a
deployment declares the extra modules it needs, the image build ships
them with their dependencies, and init loads them at boot.

The declaration lives on the Machine, as spec.modules, a list of
module names. The design weighed two candidates, and the Machine won.
The machine that has the hardware is the one that needs the driver.
A declaration in the Kubernetes API also gets everything the API
already provides: review in the deployment's own git history, staged
convergence when the declaration changes, and a status that can report
whether the request was honored. The alternative, a build input beside
the manifests, would ship the same bytes but leave the declaration
outside the API. Every machine would load everything, with no way to
report a module that never arrived.

The bytes still have to be in the image, and the build already has
what it needs to put them there: the deployment's manifests are an
input to every image build. A small program in the image domain,
inventory, reads them with the same strict parser that init uses. This
means build-time parsing and boot-time parsing can never disagree, and
a misspelled field fails the build. inventory prints the union of
every machine's declared modules. The build feeds that union through
the same modprobe --show-depends pipeline that ships the fixed list,
so dependencies come along and depmod indexes exactly what shipped. A
name that the vendored kernel has never heard of fails the build on
the spot: a deployment finds out about a typo at build time, not on a
booted fleet. This also answers how the list stays reviewable once it
is no longer fixed: the OS's own list stays fixed in this repo, the
deployment's extras are reviewed in the deployment's manifests, and
the build prints the union it shipped.

init loads declared modules in a second pass, separate from the fixed
list. The fixed list loads early, before storage settles, because the
boot path itself depends on it (vfat and its codepage modules are what
mount the FAT32 system slots). The declared list cannot load until
init determines which manifest won this boot, and that requires
storage: manifest selection happens on machineState. This ordering
also states
a boundary plainly: spec.modules is for workload hardware. A driver
that the boot path needs, anything that machineState itself depends
on, must be in the fixed list, because by the time init can read a
Machine manifest, the boot path has already run.

Each declared module resolves to one of four outcomes, and none of
them stops the boot. Loaded means the kernel accepted it, or already
had it. Builtin means the name is compiled into the vendored kernel,
so there is nothing to load and nothing wrong. The image ships
modules.builtin for exactly this reason, so init can tell a builtin
module apart from a missing one. Missing means the booted image never
shipped the module, which happens when a manifest is edited after its
image was built. The message states this and names the fix: rebuild
the deployment's image, or move spec.version to a release built from
manifests that declare the module. Failed means the module shipped but
the kernel refused it, and the hardware itself explains that outcome.
init prints one console line for each module and publishes the same
outcomes through the facts file, so status shows what the console
showed.

Convergence follows the pattern already set by storage. The boot
records which modules the winning manifest declared, as
status.boot.modules, the same way it records the storage layout it
actuated: the reference for drift is what this boot actually did, not
what the current spec says. The operator compares spec.modules against
this record. A difference stages the manifest and requests a reboot
turn, like any other machine change. Without that comparison, an edit
to modules would lie dormant until some unrelated reboot happened,
while a workload waited on a driver. One split matters and is
deliberate: SpecConverged reports whether this boot ran the manifest,
and it can be Converged while the new ModulesLoaded condition is
False, because a spec that the boot honored can still name modules
that the image never carried. The condition, not the convergence
machinery, states that the image needs a rebuild.

Each layer of validation checks only what it can actually know. The
CRD checks shape only: module names are free-form (xt_MASQUERADE is a
real one),
so admission enforces a character pattern and a reasonable count,
nothing more. Existence is checked only where existence is knowable:
at build time for the manifests the image bakes in, and at boot,
reported through status, for edits that arrive later.

The lab proved all of this, including the failure paths. node-4
declares dummy, the kernel's placeholder network driver, as its
standing example. The build log printed the declared union, the
console printed "modules: dummy: loaded", and the Machine reported
Loaded with ModulesLoaded: True. A live edit adding nbd, a real module
that the running image never shipped, staged with the drift spelled
out ("nbd declared but this boot ran without it"), rebooted on its
conductor-granted turn, and returned exactly the expected result:
SpecConverged True, ModulesLoaded False with the rebuild message,
phase Degraded, and the same verdict on the serial console. Reverting
the edit drifted the other way ("nbd no longer declared but this boot
ran with it"), rebooted, and returned the machine to Ready. A garbage
module name in the deployment's manifests failed make image at the
modprobe line, and named the module. One repair came out of the
drills: the deployment's manifests became prerequisites of the image
archive in the image domain's own Makefile, because a build that reads
the manifests must rebuild when the manifests change.

This milestone runs before milestone 17: the features design in that
milestone depends on this one, and ships each opt-in feature's kernel
half through the same pipeline.
