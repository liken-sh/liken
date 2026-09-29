# Requestable kernel modules

Milestone 18. Completed. A Machine declares the extra kernel modules
that its workloads need, the image build ships them, and init loads
them at boot.

The vendored kernel is Ubuntu's generic build, so the package that
liken fetches has a module for nearly every driver that Linux
supports. modules.conf is the fixed, reviewable list of the few
modules that the image ships and init loads: the OS's own needs, with
a reason on each entry. A machine whose workloads use hardware (a GPU
for transcoding, a USB accelerator, a capture card) also needs its
drivers. Before this milestone, the only way to get them was to edit
the OS. This milestone adds the general mechanism: a deployment
declares the extra modules that it needs, the image build ships them
with their dependencies, and init loads them at boot.

The declaration is spec.modules on the Machine, a list of module
names. The design compared two places for it, and the Machine won. The
machine that has the hardware is the machine that needs the driver. A
declaration in the Kubernetes API also gets what the API already
provides: review in the deployment's own git history, staged
convergence when the declaration changes, and a status that reports
whether the request was honored. The alternative, a build input beside
the manifests, ships the same bytes but leaves the declaration outside
the API. Every machine would load everything, with no way to report a
module that never arrived.

The bytes must still be in the image, and the build has what it needs
to put them there: the deployment's manifests are an input to every
image build. A small program in the image domain, inventory, reads
them with the same strict parser that init uses. Build-time parsing
and boot-time parsing can then never disagree, and a misspelled field
fails the build. inventory prints the union of every machine's
declared modules. The build feeds that union through the same modprobe
--show-depends pipeline that ships the fixed list, so dependencies come
with it and depmod indexes exactly what shipped. A name that the
vendored kernel does not have fails the build immediately, so a
deployment finds a typo at build time, not on a booted fleet.

The list stays reviewable although it is no longer fixed. The OS's own
list stays fixed in this repo, the deployment's extras are reviewed in
the deployment's manifests, and the build prints the union that it
shipped.

init loads declared modules in a second pass, separate from the fixed
list. The fixed list loads early, before storage settles, because the
boot path itself depends on it: vfat and its codepage modules mount the
FAT32 system slots. The declared list cannot load until init determines
which manifest won this boot, and that requires storage, because
manifest selection happens on machineState. This order also sets a
boundary: spec.modules is for workload hardware. A driver that the boot
path needs, anything that machineState itself depends on, must be in
the fixed list. By the time that init can read a Machine manifest, the
boot path has already run.

Each declared module gets one of four outcomes, and none of them stops
the boot. Loaded means the kernel accepted it, or already had it.
Builtin means the name is compiled into the vendored kernel, so there
is nothing to load and nothing wrong. The image ships modules.builtin
for this reason, so init can tell a builtin module from a missing one.
Missing means the booted image never shipped the module, which happens
when a manifest is edited after its image was built. The message states
this and names the fix: rebuild the deployment's image, or move
spec.version to a release built from manifests that declare the module.

Failed means the module shipped but the kernel refused it, and the
hardware itself explains that outcome. init prints one console line for
each module and publishes the same outcomes through the facts file, so
status shows what the console showed.

Convergence follows the pattern that storage set. The boot records
which modules the winning manifest declared, as status.boot.modules,
the same way it records the storage layout that it actuated. The
reference for drift is what this boot did, not what the current spec
says. The operator compares spec.modules against this record. A
difference stages the manifest and requests a reboot turn, like any
other machine change. Without that comparison, an edit to modules would
stay dormant until some unrelated reboot, while a workload waited on a
driver.

One split matters and is deliberate. SpecConverged reports whether this
boot ran the manifest, and it can be Converged while the ModulesLoaded
condition is False, because a spec that the boot honored can still name
modules that the image never shipped. The condition, not the
convergence machinery, states that the image needs a rebuild.

Each layer of validation checks only what is available to it. The CRD
checks shape only. Module names are free-form (xt_MASQUERADE is a real one),
so admission enforces a character pattern and a reasonable count, and
nothing more. Existence is checked where it can be: at
build time for the manifests that the image bakes in, and at boot,
reported through status, for edits that arrive later.

The lab proved every path, including the failures. node-4 declares
dummy, the kernel's placeholder network driver, as its standing
example. The build log printed the declared union, the console printed
"modules: dummy: loaded", and the Machine reported Loaded with
ModulesLoaded: True. A live edit that added nbd, a real module that the
running image never shipped, staged with the drift stated ("nbd
declared but this boot ran without it"), rebooted on its
conductor-granted turn, and gave the expected result: SpecConverged
True, ModulesLoaded False with the rebuild message, phase Degraded, and
the same verdict on the serial console. A revert of the edit drifted
the other way ("nbd no longer declared but this boot ran with it"),
rebooted, and returned the machine to Ready. A garbage module name in
the deployment's manifests failed make image at the modprobe line, and
named the module.

One repair came out of the drills. The deployment's manifests became
prerequisites of the image archive in the image domain's own Makefile,
because a build that reads the manifests must rebuild when the
manifests change.

This milestone runs before milestone 17. The features design in
milestone 17 depends on this one, and ships each opt-in feature's
kernel half through the same pipeline.
