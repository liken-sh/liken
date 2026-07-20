package machine

// Drift compares what a Machine spec declares against what a boot
// actuated. This package holds shared logic, not logic specific to
// the operator, because two programs must agree on the comparison
// exactly. The operator uses the comparison to decide whether to
// converge, and how. init uses the comparison to check that a staged
// manifest is really applicable without a reboot, before init
// applies the manifest live. If the operator and init used two
// different implementations of "is this the same storage?", the two
// implementations would eventually disagree. The operator would then
// request an action forever that init keeps refusing.

import (
	"fmt"
	"maps"
	"slices"
)

// StorageDrift compares the declared storage against what the boot
// actuated. It compares role by role and normalizes sizes, so
// 2048Mi and 2Gi declare the same thing. StorageDrift writes the
// returned diffs for people to read. The diffs appear verbatim in
// condition messages.
func StorageDrift(desired, actuated StorageSpec) []string {
	var diffs []string
	desiredRoles := rolesByName(desired)
	actuatedRoles := rolesByName(actuated)
	for _, name := range StorageRoleNames {
		d, dok := desiredRoles[name]
		a, aok := actuatedRoles[name]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("%s: declared but not actuated", name))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("%s: actuated but no longer declared", name))
		case dok && aok:
			if d.Device != a.Device {
				diffs = append(diffs, fmt.Sprintf("%s: device %s declared, %s actuated", name, d.Device, a.Device))
			}
			if !sameSize(d.Size, a.Size) {
				diffs = append(diffs, fmt.Sprintf("%s: size %s declared, %s actuated", name, orRemainder(d.Size), orRemainder(a.Size)))
			}
		}
	}
	return diffs
}

// ModuleSetDiff compares two module lists as sets. Order and
// repetition carry no meaning in these lists. ModuleSetDiff reports
// both directions separately, because the two directions converge in
// different ways. The system can load an added module into the
// running kernel. But a retracted module can only leave the system
// at a reboot. The kernel has no safe way to remove a driver while
// something else uses it.
func ModuleSetDiff(desired, actuated []string) (added, retracted []string) {
	want := map[string]bool{}
	for _, name := range desired {
		want[name] = true
	}
	have := map[string]bool{}
	for _, name := range actuated {
		have[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if !have[name] {
			added = append(added, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(have)) {
		if !want[name] {
			retracted = append(retracted, name)
		}
	}
	return added, retracted
}

// ModulesDrift writes ModuleSetDiff results for people to read, the
// same way StorageDrift does. The actuated side is the boot record's
// copy of the request, not the result of loading modules. This
// design is deliberate. A declared module that the image lacked
// still counts as actuated, because rebooting again with the same
// image would change nothing. The ModulesLoaded condition reports
// that problem instead. The fix for that problem is a new image, not
// a reboot.
func ModulesDrift(desired, actuated []string) []string {
	added, retracted := ModuleSetDiff(desired, actuated)
	var diffs []string
	for _, name := range added {
		diffs = append(diffs, fmt.Sprintf("modules: %s declared but this boot ran without it", name))
	}
	for _, name := range retracted {
		diffs = append(diffs, fmt.Sprintf("modules: %s no longer declared but this boot ran with it", name))
	}
	return diffs
}

func rolesByName(spec StorageSpec) map[StorageRoleName]DeclaredRole {
	byName := map[StorageRoleName]DeclaredRole{}
	for _, role := range spec.Roles() {
		byName[role.Name] = role
	}
	return byName
}

// sameSize compares two size declarations by the number of bytes
// each one describes, not by the spelling. If a size cannot be
// parsed, sameSize falls back to a string comparison instead of a
// panic. Validation refuses an unparseable size anyway.
func sameSize(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	aBytes, aErr := ParseSize(a)
	bBytes, bErr := ParseSize(b)
	if aErr != nil || bErr != nil {
		return a == b
	}
	return aBytes == bBytes
}

func orRemainder(size string) string {
	if size == "" {
		return "(remainder)"
	}
	return size
}
