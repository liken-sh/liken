package machine

// Drift: comparing what a Machine spec declares against what a boot
// actuated. These are shared grammar rather than operator logic,
// because two programs must agree on them exactly: the operator uses
// them to decide whether (and how) to converge, and init uses them
// to verify that a staged manifest really is applicable without a
// reboot before it applies one live. Two implementations of "is
// this the same storage?" would eventually disagree, and the
// disagreement would surface as an operator requesting forever what
// init keeps refusing.

import (
	"fmt"
	"maps"
	"slices"
)

// StorageDrift compares the declared storage against what the boot
// actuated, role by role, with sizes normalized (2048Mi and 2Gi
// declare the same thing). The returned diffs are written for
// humans; they appear verbatim in condition messages.
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

// ModuleSetDiff compares two module lists as sets — order and
// repetition carry no meaning — and reports both directions
// separately, because they converge differently: an added module
// can be loaded into the running kernel, while a retracted one can
// only leave at a reboot (the kernel offers no safe way to pull a
// driver out from under whatever started using it).
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

// ModulesDrift renders ModuleSetDiff for humans, in the same voice
// as StorageDrift. The actuated side is the boot record's copy of
// the ask, not the load outcomes, and that is deliberate: a
// declared module the image lacked still counts as actuated,
// because rebooting again with the same image would change nothing.
// The ModulesLoaded condition is what reports that problem, and its
// fix is a new image, not a reboot.
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
// they describe rather than by their spelling. An unparseable size
// (which validation will refuse anyway) falls back to string
// comparison rather than panicking here.
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
