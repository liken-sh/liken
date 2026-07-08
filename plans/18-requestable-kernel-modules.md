# Requestable kernel modules

Milestone 18 — Done

The vendored kernel is Ubuntu's generic build, so nearly every driver
Linux has already exists as a module in the package we fetch.
modules.conf is the deliberately fixed, reviewable list of the few the
image ships and init loads: the OS's own needs, each entry annotated
with its reason. A machine whose workloads use hardware (a GPU for
transcoding, a USB accelerator, a capture card) needs its drivers too,
and the only way to get them today is to edit the OS. This milestone
adds the universal primitive: a deployment declares the extra modules
it wants, the image build ships them with their dependencies, and init
loads them at boot.

The declaration lives on the Machine, as spec.modules, a list of
module names. Of the two candidates the design weighed, the Machine
wins because the machine that has the hardware is the one that needs
the driver, and because a declaration in the Kubernetes API gets
everything the API already provides: review in the deployment's own
git history, staged convergence when it changes, and a status that can
say whether the ask was honored. The alternative, a build input beside
the manifests, would ship the same bytes but leave the declaration
outside the API, with every machine loading everything and no way to
report a module that never arrived.

The bytes still have to be in the image, and the build already has
what it needs to put them there: the deployment's manifests are an
input to every image build. A small program in the image domain,
inventory, reads them with the same strict parser init uses (so
build-time parsing and boot-time parsing can never disagree, and a
misspelled field fails the build) and prints the union of every
machine's declared modules. The build feeds that union through the
same modprobe --show-depends pipeline that ships the fixed list, so
dependencies come along and depmod indexes exactly what shipped. A
name the vendored kernel has never heard of fails the build on the
spot: a deployment finds out about a typo at build time, not on a
booted fleet. That is also the answer to how the list stays reviewable
once it is no longer fixed: the OS's own list stays fixed in this
repo, the deployment's extras are reviewed in the deployment's
manifests, and the build prints the union it shipped.

Init loads declared modules in a second pass, separate from the fixed
list. The fixed list loads early, before storage settles, because the
boot path itself depends on it (vfat and its codepage modules are what
mount the FAT32 system slots). The declared list cannot load until
init knows which manifest won this boot, and that takes storage:
manifest selection happens on machineState. The ordering is also a
boundary worth stating plainly: spec.modules is for workload hardware.
A driver the boot path needs, anything machineState itself sits
behind, must be in the fixed list, because by the time init can read a
Machine manifest the boot path has already run.

Each declared module resolves to one of four outcomes, and none of
them stops the boot. Loaded means the kernel took it, or already had
it. Builtin means the name is compiled into the vendored kernel, so
there is nothing to load and nothing wrong; the image ships
modules.builtin precisely so init can tell this apart from a missing
module. Missing means the booted image never shipped it, which happens
when a manifest is edited after its image was built; the message says
so and names the fix: rebuild the deployment's image, or move
spec.version to a release built from manifests that declare it. Failed
means the module shipped but the kernel refused it, which is the
hardware's story to tell. Init prints one console line per module and
publishes the same outcomes through the facts file, so status shows
what the console showed.

Convergence follows the storage precedent. The boot records which
modules the winning manifest declared, as status.boot.modules, the
same way it records the storage layout it actuated: the drift
reference is what this boot actually did, not what the current spec
says. The operator compares spec.modules against it, and a difference
stages the manifest and requests a reboot turn like any other machine
change; without that comparison a modules edit would lie dormant until
some unrelated reboot happened along, while a workload waits on a
driver. One split matters and is deliberate: SpecConverged reports
whether this boot ran the manifest, and it can be Converged while the
new ModulesLoaded condition is False, because a spec the boot honored
can still name modules the image never carried. The condition, not the
convergence machinery, is what says the image needs rebuilding.

Validation is honest about what each layer can know. The CRD checks
shape only: module names are free-form (xt_MASQUERADE is a real one),
so admission enforces a character pattern and a sane count, nothing
more. Existence is checked where existence is knowable: at build time
for the manifests the image bakes, and at boot, reported through
status, for edits that arrived later.

The lab proved all of it, failure paths included. node-4 declares
dummy, the kernel's placeholder network driver, as its standing
example: the build log printed the declared union, the console printed
"modules: dummy: loaded", and the Machine reported Loaded with
ModulesLoaded: True. A live edit adding nbd, a real module the running
image never shipped, staged with the drift spelled out ("nbd declared
but this boot ran without it"), rebooted on its conductor-granted
turn, and came back exactly as designed: SpecConverged True,
ModulesLoaded False with the rebuild message, phase Degraded, the same
verdict on the serial console. Reverting the edit drifted the other
way ("nbd no longer declared but this boot ran with it"), rebooted,
and returned the machine to Ready. And a garbage module name in the
deployment's manifests failed make image at the modprobe line, naming
the module. One repair came out of the drills: the deployment's
manifests became prerequisites of the image archive in the image
domain's own Makefile, because a build that reads the manifests must
rebuild when they change.

This milestone runs before 17: the features design there rides on this
one, shipping each opt-in feature's kernel half through the same
pipeline.
