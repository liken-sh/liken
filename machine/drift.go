package machine

// Drift compares what a Machine spec declares against what a boot
// actuated. This package holds shared logic, not logic specific to
// the operator, because two programs must agree on the comparison
// exactly. The operator uses the comparison to determine whether to
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
	"strings"
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

// NetworkDrift compares the declared network against what the boot
// actuated. Unlike storage, a network spec has no grow-only rule and
// no shape a machine must keep: any spec may replace any other, and
// the only question is whether the running machine matches the one
// the cluster asks for now.
//
// A nil actuated spec means the boot recorded no network. There is
// nothing to compare against, so there is no drift to report. The
// alternative would be to read every declared interface as drift, and
// stage a manifest and ask for a reboot on every machine whose facts
// happen to lack this record.
//
// The comparison walks both lists by position, because the order of
// this list carries meaning: interface order is the order that each
// interface's nameservers reach resolv.conf in. Two specs that name
// the same ports in a different order are two different requests. The
// name at each position says which port that position asks for.
func NetworkDrift(desired NetworkSpec, actuated *NetworkSpec) []string {
	if actuated == nil {
		return nil
	}
	var diffs []string
	for i := range max(len(desired.Interfaces), len(actuated.Interfaces)) {
		switch {
		case i >= len(actuated.Interfaces):
			diffs = append(diffs, fmt.Sprintf("network: %s declared but not actuated", desired.Interfaces[i].Name))
		case i >= len(desired.Interfaces):
			diffs = append(diffs, fmt.Sprintf("network: %s actuated but no longer declared", actuated.Interfaces[i].Name))
		default:
			diffs = append(diffs, interfaceDrift(i, desired.Interfaces[i], actuated.Interfaces[i])...)
		}
	}
	return diffs
}

// interfaceDrift compares one position of the two lists. When the two
// entries name different ports, that one difference is the whole
// report: the addressing under it belongs to another port, so
// reporting each field as well would say the same thing several times
// over.
func interfaceDrift(position int, desired, actuated InterfaceSpec) []string {
	if desired.Name != actuated.Name {
		return []string{fmt.Sprintf("network: interface %d: %s declared, %s actuated",
			position+1, desired.Name, actuated.Name)}
	}
	var diffs []string
	if desired.Address != actuated.Address {
		diffs = append(diffs, fmt.Sprintf("network: %s: address %s declared, %s actuated",
			desired.Name, orDHCP(desired.Address), orDHCP(actuated.Address)))
	}
	if desired.Gateway != actuated.Gateway {
		diffs = append(diffs, fmt.Sprintf("network: %s: gateway %s declared, %s actuated",
			desired.Name, orNone(desired.Gateway), orNone(actuated.Gateway)))
	}
	if !slices.Equal(desired.Nameservers, actuated.Nameservers) {
		diffs = append(diffs, fmt.Sprintf("network: %s: nameservers %s declared, %s actuated",
			desired.Name,
			orNone(strings.Join(desired.Nameservers, ", ")),
			orNone(strings.Join(actuated.Nameservers, ", "))))
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

// orDHCP names the empty address for a reader. An interface with no
// declared address asks for DHCP, so the diff says so rather than
// leaving a gap where a value should be.
func orDHCP(address string) string {
	if address == "" {
		return "(DHCP)"
	}
	return address
}

// orNone names an empty optional field for a reader, for the same
// reason orDHCP does.
func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
