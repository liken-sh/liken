package main

// Loading a staged spec's modules into the running kernel.
//
// Most machine-spec changes converge by reboot, because most of what
// the spec declares (storage above all) can only be actuated by a
// boot. Adding a module is the exception. Loading is live-capable:
// the kernel binds a resident driver to already-plugged hardware on
// its own, in either order. So an additive spec.modules edit
// converges here, in place, with nothing drained and nothing
// restarted. The operator stages the manifest as it would for a
// reboot (durability: the next boot must load the same list) and
// writes a modules intent. This file is init's response.
//
// The intent is only a signal. The staged store is the truth about
// what to apply, and init re-derives the manifest's live-applicability
// for itself, with the same shared drift functions that the operator
// used: identical storage, and no module retracted. This re-derivation
// is what makes a stale, duplicate, or malicious intent harmless.
// Anything that would need a boot is refused and left staged for a
// boot.
//
// Promotion comes after the loads, deliberately. A module that
// panics the kernel on load takes the machine down in the middle of
// applying, and the manifest it came from must still be staged when
// the machine comes back. The next boot tries it once, fails the
// same way, and the rejection machinery quarantines it (staging.go).
// Promoting first would enshrine a kernel-crashing spec as proven. A
// load that merely fails (the kernel refuses the module) is not that
// case. The outcome is recorded, and the spec still promotes, exactly
// as a boot would treat it, because retrying the same load changes
// nothing, and the ModulesLoaded condition carries the record.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// moduleLoader owns the subtrees a live load rewrites: modules/,
// boot/modules, and boot/manifest. It holds the boot's values so a load
// judges the staged spec against what actually booted, without reading
// anything back from the tree. The struct is the module loader's whole
// state, seeded from the boot and updated by each successful load.
type moduleLoader struct {
	tree        machine.FactsTree
	bootStorage machine.StorageSpec
	bootModules []string
	statuses    []machine.ModuleStatus
}

// apply performs one live load. It reads the staged manifest, refuses
// it unless it is boot-equivalent plus added modules, loads the
// additions, promotes the manifest, and republishes the facts so the
// operator sees convergence. Every refusal is only a console line. The
// manifest stays staged for the reboot that can actually apply it.
func (l *moduleLoader) apply(intent machine.ModulesIntent, store machine.ManifestStore, moduleBase string) {
	raw, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading the staged spec: %v\n", err)
		return
	}
	if raw == nil {
		// A stale signal: the staged spec is already promoted, or
		// withdrawn. The operator's next pass sees the truth.
		return
	}
	doc, err := machine.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: parsing the staged spec: %v\n", err)
		return
	}
	hash := machine.ManifestHash(raw)
	if intent.ManifestHash != "" && intent.ManifestHash != hash {
		// The store moved on since the intent was written. The newer
		// staged bytes are what the operator wants now, so this code
		// judges those bytes. The hash difference is only worth
		// reporting.
		fmt.Printf("liken: modules: the staged spec (%.12s) is newer than the intent (%.12s); applying the store's copy\n",
			hash, intent.ManifestHash)
	}

	if diffs := machine.StorageDrift(doc.Spec.Storage, l.bootStorage); len(diffs) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) changes storage (%s); it needs a boot, not a load\n",
			hash, strings.Join(diffs, "; "))
		return
	}
	added, retracted := machine.ModuleSetDiff(doc.Spec.Modules, l.bootModules)
	if len(retracted) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) retracts %s; loading is one-way, so it needs a boot\n",
			hash, strings.Join(retracted, ", "))
		return
	}

	outcomes := loadDeclaredModulesFrom(moduleBase, added)

	if err := store.Promote(); err != nil {
		// The loads happened, but the record did not move. The
		// operator re-requests, the loads re-run as no-ops (an
		// already-loaded module returns EEXIST, which counts as
		// loaded), and promotion gets another try.
		fmt.Fprintf(os.Stderr, "liken: modules: promoting the applied spec: %v\n", err)
		return
	}

	l.bootModules = slices.Sorted(slices.Values(doc.Spec.Modules))
	l.statuses = mergeModuleStatuses(l.statuses, outcomes)
	// The write order is the commit protocol. boot/modules and modules/
	// land first, and boot/manifest lands last, because the manifest
	// record is the commit point the operator's convergence judges
	// (machine-operator/converge.go). The operator must never read a
	// promoted manifest hash before the module facts that explain it.
	l.tree.WriteBootModules(l.bootModules)
	l.tree.WriteModules(l.statuses)
	l.tree.WriteBootManifest(machine.ManifestSourceProven, hash)
	fmt.Printf("liken: spec %.12s applied in place: %s loaded without a reboot\n",
		hash, strings.Join(added, ", "))
}

// mergeModuleStatuses folds a load's outcomes into the standing
// per-module report. A fresh outcome replaces its module's old
// entry, and everything else stays unchanged. The result stays
// sorted by name, so the facts are stable across publishes.
func mergeModuleStatuses(standing, fresh []machine.ModuleStatus) []machine.ModuleStatus {
	byName := map[string]machine.ModuleStatus{}
	for _, s := range standing {
		byName[s.Name] = s
	}
	for _, s := range fresh {
		byName[s.Name] = s
	}
	names := slices.Sorted(maps.Keys(byName))
	merged := make([]machine.ModuleStatus, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name])
	}
	return merged
}
