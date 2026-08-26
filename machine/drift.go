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
// HostEntries takes no part in this comparison. Unlike an interface,
// a host entry reconciles live (milestone 53): the machine operator
// applies spec.network.hostEntries on every pass, so an edit never
// waits for a reboot and never belongs in the set of differences
// that asks for one.
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
	if wirelessSummary(desired.Wireless) != wirelessSummary(actuated.Wireless) {
		diffs = append(diffs, fmt.Sprintf("network: %s: wireless %s declared, %s actuated",
			desired.Name,
			orNone(wirelessSummary(desired.Wireless)),
			orNone(wirelessSummary(actuated.Wireless))))
	}
	return diffs
}

// wirelessSummary renders one wireless entry as the pair of facts
// that decide whether a rejoin is needed. The security is resolved
// through its default first, so a spec that left the field unset and
// a record that holds the default read as the same request instead
// of as drift that never settles.
func wirelessSummary(w *WirelessSpec) string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s)", w.SSID, w.SecurityOrDefault())
}

// RlimitDrift compares the declared resource limits against what the
// boot actuated. Both sides are the spec's own map, so this is a
// straight comparison of two requests, the way ModulesDrift compares
// two module lists.
//
// The limits every liken machine holds take no part in this. They ship
// with the release rather than with the spec, so a change to the table
// arrives with a new system image and the reboot that installs it.
// Comparing them here would read as drift on every machine in a fleet
// the moment the table changed.
//
// A value is compared as written, not as parsed. "1048576" and
// "1048576:1048576" set the same pair of numbers, and this reports
// them as drift. That costs one reboot on a machine whose operator
// rewrote a value without changing it, and the alternative costs a
// parse in the one comparison that decides whether to reboot a
// machine. A drift that reboots once too often is a smaller fault than
// one that never reboots at all.
func RlimitDrift(desired, actuated map[string]string) []string {
	var diffs []string
	names := map[string]bool{}
	for name := range desired {
		names[name] = true
	}
	for name := range actuated {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		d, dok := desired[name]
		a, aok := actuated[name]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s declared but not actuated", name, d))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s actuated but no longer declared", name, a))
		case d != a:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s declared, %s actuated", name, d, a))
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
//
// Parameter drift rides in this same list rather than in a list of
// its own, because the live-load rule counts: a staged spec applies
// live only when added modules are the whole of the drift
// (converge.go). A parameter line in the count makes any parameter
// change on a loaded module miss that test and take the reboot path,
// with no new rule written anywhere.
func ModulesDrift(desired, actuated []string, desiredParameters, actuatedParameters map[string]string) []string {
	added, retracted := ModuleSetDiff(desired, actuated)
	var diffs []string
	for _, name := range added {
		diffs = append(diffs, fmt.Sprintf("modules: %s declared but this boot ran without it", name))
	}
	for _, name := range retracted {
		diffs = append(diffs, fmt.Sprintf("modules: %s no longer declared but this boot ran with it", name))
	}
	return append(diffs, ModuleParameterDrift(desired, actuated, desiredParameters, actuatedParameters)...)
}

// ModuleParameterDrift writes a line only for a module that both the
// desired and the actuated sets declare. A parameter that arrives
// with a module the boot never loaded is part of that module's own
// added line: the live loader passes the string at the load, because
// a module loading for the first time takes its parameters normally.
// A parameter on a module the boot already loaded writes its own
// line here, and that line is what forces the reboot path, because a
// loaded module never reads its parameters again.
func ModuleParameterDrift(desiredModules, actuatedModules []string, desired, actuated map[string]string) []string {
	shared := sharedModules(desiredModules, actuatedModules)
	keys := map[string]bool{}
	for key := range desired {
		keys[key] = true
	}
	for key := range actuated {
		keys[key] = true
	}
	var diffs []string
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		module, _, ok := splitModuleParameterKey(key)
		if !ok || !shared[module] {
			continue
		}
		d, dok := desired[key]
		a, aok := actuated[key]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s declared but not actuated", key, d))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s actuated but no longer declared", key, a))
		case d != a:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s declared, %s actuated", key, d, a))
		}
	}
	return diffs
}

// sharedModules is the set of modules present in both lists, the
// only modules a parameter drift line may name.
func sharedModules(desired, actuated []string) map[string]bool {
	have := map[string]bool{}
	for _, name := range actuated {
		have[name] = true
	}
	shared := map[string]bool{}
	for _, name := range desired {
		if have[name] {
			shared[name] = true
		}
	}
	return shared
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
