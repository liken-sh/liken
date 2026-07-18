package main

// Loading a staged spec's modules into the running kernel.
//
// Most machine-spec changes converge by reboot, because most of what
// the spec declares (storage above all) can only be actuated by a
// boot. Adding a module is the exception: loading is live-capable —
// the kernel binds a resident driver to already-plugged hardware on
// its own, in either order — so an additive spec.modules edit
// converges here, in place, with nothing drained and nothing
// restarted. The operator stages the manifest as it would for a
// reboot (durability: the next boot must load the same list) and
// writes a modules intent; this file is init's answer.
//
// The intent is only the doorbell. The staged store is the truth
// about what to apply, and init re-derives the manifest's
// live-applicability for itself with the same shared drift functions
// the operator used: identical storage, no module retracted. That
// re-derivation is what makes a stale, duplicate, or malicious
// intent harmless — anything that would need a boot is refused and
// left staged for one.
//
// Promotion comes after the loads, deliberately. A module that
// panics the kernel on load takes the machine down mid-apply, and
// the manifest it came from must still be *staged* when the machine
// comes back: the next boot tries it once, fails the same way, and
// the rejection machinery quarantines it (staging.go). Promoting
// first would enshrine a kernel-crashing spec as proven. A load that
// merely fails (the kernel refuses the module) is not that case: the
// outcome is recorded and the spec still promotes, exactly as a boot
// would treat it, because retrying the same load changes nothing and
// the ModulesLoaded condition carries the story.

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/liken-sh/liken/machine"
)

// applyModulesIntent performs one live load: read the staged
// manifest, refuse it unless it is boot-equivalent plus added
// modules, load the additions, promote, and republish the facts so
// the operator sees convergence. Every refusal is a console line
// and nothing else; the manifest stays staged for the reboot that
// can actually apply it.
func applyModulesIntent(intent machine.ModulesIntent, store machine.ManifestStore, moduleBase string, facts *factsFile) {
	raw, err := store.LoadStaged()
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: reading the staged spec: %v\n", err)
		return
	}
	if raw == nil {
		// A stale doorbell: the staged spec is already promoted (or
		// withdrawn). The operator's next pass sees the truth.
		return
	}
	doc, err := machine.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "liken: modules: parsing the staged spec: %v\n", err)
		return
	}
	hash := machine.ManifestHash(raw)
	if intent.ManifestHash != "" && intent.ManifestHash != hash {
		// The store moved on since the intent was written; the newer
		// staged bytes are what the operator wants now, so judge
		// those. The hash difference is only worth narrating.
		fmt.Printf("liken: modules: the staged spec (%.12s) is newer than the intent (%.12s); applying the store's copy\n",
			hash, intent.ManifestHash)
	}

	var bootStorage machine.StorageSpec
	var bootModules []string
	facts.mutate(func(s *machine.MachineStatus) {
		bootStorage = s.Boot.Storage
		bootModules = slices.Clone(s.Boot.Modules)
	})

	if diffs := machine.StorageDrift(doc.Spec.Storage, bootStorage); len(diffs) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) changes storage (%s); it needs a boot, not a load\n",
			hash, strings.Join(diffs, "; "))
		return
	}
	added, retracted := machine.ModuleSetDiff(doc.Spec.Modules, bootModules)
	if len(retracted) != 0 {
		fmt.Printf("liken: modules: the staged spec (%.12s) retracts %s; loading is one-way, so it needs a boot\n",
			hash, strings.Join(retracted, ", "))
		return
	}

	outcomes := loadDeclaredModulesFrom(moduleBase, added)

	if err := store.Promote(); err != nil {
		// The loads happened but the record didn't move: the operator
		// re-requests, the loads re-run as no-ops (an already-loaded
		// module is EEXIST, which counts as loaded), and promotion
		// gets another try.
		fmt.Fprintf(os.Stderr, "liken: modules: promoting the applied spec: %v\n", err)
		return
	}
	facts.publish(func(s *machine.MachineStatus) {
		s.Boot.Modules = slices.Sorted(slices.Values(doc.Spec.Modules))
		s.Boot.ManifestHash = hash
		s.Boot.ManifestSource = machine.ManifestSourceProven
		s.Modules = mergeModuleStatuses(s.Modules, outcomes)
	})
	fmt.Printf("liken: spec %.12s applied in place: %s loaded without a reboot\n",
		hash, strings.Join(added, ", "))
}

// mergeModuleStatuses folds a load's outcomes into the standing
// per-module report: a fresh outcome replaces its module's old
// entry, everything else stands. The result stays sorted by name so
// the facts are stable across publishes.
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
