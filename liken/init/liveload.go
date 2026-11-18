package main

// Init loads a staged spec's modules into the running kernel.
//
// Most spec changes take effect only on a reboot, because init applies
// storage and most other settings during boot. A new kernel module does
// not need one: the kernel binds a driver that loads late to devices
// that are already present. So when a staged spec only adds entries to
// spec.modules, init loads them in place, with no drain and no restart.
// The same load declares any added spec.serio entries, and the serio
// watch in serio.go attaches them.
//
// machine-operator stages the manifest as it does for a reboot, so the
// next boot loads the same list, and writes a modules intent. The
// intent is only a trigger. Init reads the staged manifest and checks
// for itself that the change can apply live, with the drift functions
// that the operator uses: the same storage and network as the boot, and
// no module removed. So a stale, duplicate, or forged intent applies
// nothing that a reboot would not. When the change needs a boot, init
// refuses it and leaves the manifest staged.
//
// The manifest is promoted after the loads. If a module panics the
// kernel while it loads, the machine goes down with the manifest still
// staged. The next boot then tries the spec, fails the same way, and
// machine/staging.go moves the manifest aside as rejected. Promoting
// first would mark a spec that crashes the kernel as proven. A module
// that the kernel refuses to load does not stop the promotion. A boot
// promotes in that case too, because the same load fails the same way
// again, and the ModulesLoaded condition reports the failure.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/machine"
)

// moduleLoader applies live loads and owns the facts files they write:
// modules/, boot/modules, and boot/manifest. It starts from the values
// the machine booted with and updates them after each successful load,
// so it compares a staged spec with what is running without reading
// the facts tree back.
type moduleLoader struct {
	tree        machine.FactsTree
	bootStorage machine.StorageSpec
	bootNetwork machine.NetworkSpec
	bootModules []string
	// bootParameters holds the module parameters that were delivered
	// to the running kernel. A staged spec's parameters are compared
	// with it.
	bootParameters map[string]string
	// bootSerio is the spec.serio list already declared, and serio is
	// the registry that a load declares added entries to.
	bootSerio []machine.SerioAttachment
	serio     *serioRegistry
	statuses  []machine.ModuleStatus
}

// apply performs one live load. It refuses the staged manifest unless
// the only differences from the boot are added modules and added serio
// entries. Then it loads the additions, promotes the manifest, and
// writes the facts that the operator reads to see the spec converge. A
// refusal prints a console line and leaves the manifest staged for a
// reboot.
func (l *moduleLoader) apply(intent machine.ModulesIntent, store machine.ManifestStore, moduleBase string) {
	raw, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading the staged spec: %v\n", err)
		return
	}
	if raw == nil {
		// The spec was already promoted or was withdrawn, so the
		// intent is stale.
		return
	}
	doc, err := machine.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: parsing the staged spec: %v\n", err)
		return
	}
	hash := machine.ManifestHash(raw)
	if intent.ManifestHash != "" && intent.ManifestHash != hash {
		// The operator staged a newer manifest after it wrote the
		// intent. The staged copy is the current spec, so apply it.
		fmt.Printf("liken: modules: the staged spec (%.12s) is newer than the intent (%.12s), so init applies the staged spec\n",
			hash, intent.ManifestHash)
	}

	if diffs := machine.StorageDrift(doc.Spec.Storage, l.bootStorage); len(diffs) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) changes storage (%s), so it needs a boot, not a load\n",
			hash, strings.Join(diffs, "; "))
		return
	}
	if diffs := machine.NetworkDrift(doc.Spec.Network, &l.bootNetwork); len(diffs) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) changes the network (%s), so it needs a boot, not a load\n",
			hash, strings.Join(diffs, "; "))
		return
	}
	added, retracted := machine.ModuleSetDiff(doc.Spec.Modules, l.bootModules)
	if len(retracted) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) removes %s, and a loaded module cannot be unloaded, so it needs a boot\n",
			hash, strings.Join(retracted, ", "))
		return
	}

	// A module reads its parameters only when it loads, so a new
	// parameter for a module that is already loaded needs a reboot.
	// ModuleParameterDrift compares only the modules in both lists, so
	// the parameters of an added module are not drift. The load below
	// passes them.
	if diffs := machine.ModuleParameterDrift(doc.Spec.Modules, l.bootModules,
		doc.Spec.ModuleParameters, l.bootParameters); len(diffs) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) changes the parameters of a loaded module (%s), so it needs a boot, not a load\n",
			hash, strings.Join(diffs, "; "))
		return
	}

	// Removing a serio entry needs a boot, like removing a module,
	// because pods may hold devices that the port created.
	addedSerio, retractedSerio := machine.SerioSetDiff(doc.Spec.Serio, l.bootSerio)
	if len(retractedSerio) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) removes a serio entry (%s), so it needs a boot, not a load\n",
			hash, strings.Join(machine.SerioDrift(doc.Spec.Serio, l.bootSerio), "; "))
		return
	}

	// ModuleSetDiff returns the added modules sorted, but a boot loads
	// them in spec.modules order, so load them in that order too. The
	// order matters because a module that loads first as a dependency
	// of another is already resident when its own turn comes, and the
	// kernel ignores the parameters of a resident module.
	load := declaredOrder(doc.Spec.Modules, added)

	outcomes := loadDeclaredModulesFrom(moduleBase, load, doc.Spec.ModuleParameters)

	if err := store.Promote(); err != nil {
		// The modules are loaded but the manifest is still staged, so
		// the operator requests the load again. The second load finds
		// each module resident (finit_module returns EEXIST, which
		// counts as loaded), and promotion is tried again.
		fmt.Fprintf(os.Stderr, "liken: modules: promoting the applied spec: %v\n", err)
		return
	}

	// boot/modules records the order the kernel loaded the modules
	// in: the boot's modules, then this load's. Copying the
	// manifest's list would record an order this machine never
	// loaded, and the operator would then report a reordered
	// spec.modules as converged.
	l.bootModules = slices.Concat(l.bootModules, load)
	l.bootParameters = deliveredParameters(doc.Spec.ModuleParameters, outcomes)
	l.bootSerio = slices.Concat(l.bootSerio, addedSerio)
	l.statuses = mergeModuleStatuses(l.statuses, outcomes)
	// Write boot/manifest last. The operator judges convergence from
	// it (machine-operator/converge.go), so it must not see the new
	// manifest hash before the module facts that go with it.
	l.tree.WriteBootModules(l.bootModules)
	l.tree.WriteBootModuleParameters(l.bootParameters)
	l.tree.WriteBootSerio(l.bootSerio)
	l.tree.WriteModules(l.statuses)
	l.tree.WriteBootManifest(machine.ManifestSourceProven, hash)
	// Declaring after boot/manifest is safe because the serio watch
	// reports each attachment in status.serio by itself, and the
	// operator's convergence check reads only the boot record above.
	if l.serio != nil && len(addedSerio) != 0 {
		l.serio.declare(l.bootSerio)
	}
	fmt.Printf("liken: spec %.12s applied in place without a reboot: %s\n",
		hash, appliedInPlace(load, addedSerio))
}

func appliedInPlace(loaded []string, attached []machine.SerioAttachment) string {
	var parts []string
	if len(loaded) != 0 {
		parts = append(parts, strings.Join(loaded, ", ")+" loaded")
	}
	for _, a := range attached {
		parts = append(parts, a.String()+" declared for attachment")
	}
	if len(parts) == 0 {
		return "nothing to load"
	}
	return strings.Join(parts, "; ")
}

// deliveredParameters returns the staged spec's parameters without
// those of modules that were already resident when this load reached
// them. finit_module returns EEXIST for a resident module and ignores
// its parameter string. With those parameters left out of the boot
// record, the operator reports them as drift on a loaded module, stages
// the manifest, and reboots the machine in its normal turn. The boot
// then loads the modules in spec.modules order with their parameters.
// The parameters of every other module stay in the record. For a
// Missing or Failed module a reboot gives the same outcome, and the
// ModulesLoaded condition reports it.
func deliveredParameters(declared map[string]string, outcomes []machine.ModuleStatus) map[string]string {
	delivered := maps.Clone(declared)
	for _, outcome := range outcomes {
		if !outcome.AlreadyResident {
			continue
		}
		for _, parameter := range machine.ModuleParameterNames(outcome.Name, declared) {
			delete(delivered, outcome.Name+"."+parameter)
		}
	}
	return delivered
}

// declaredOrder returns the added modules in the order that
// spec.modules lists them.
func declaredOrder(declared, added []string) []string {
	want := map[string]bool{}
	for _, name := range added {
		want[name] = true
	}
	order := make([]string, 0, len(added))
	for _, name := range declared {
		if want[name] {
			order = append(order, name)
		}
	}
	return order
}

// mergeModuleStatuses adds the outcomes of a load to the published
// module statuses. An outcome replaces the status of the same module.
// The result is sorted by name, so the published facts keep their
// order between writes.
func mergeModuleStatuses(published, outcomes []machine.ModuleStatus) []machine.ModuleStatus {
	byName := map[string]machine.ModuleStatus{}
	for _, s := range published {
		byName[s.Name] = s
	}
	for _, s := range outcomes {
		byName[s.Name] = s
	}
	names := slices.Sorted(maps.Keys(byName))
	merged := make([]machine.ModuleStatus, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name])
	}
	return merged
}
