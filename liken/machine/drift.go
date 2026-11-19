package machine

// machine-operator and init both compare a Machine spec against the
// boot record, the copy of the spec that this boot applied, so the
// comparison is in this package. The
// operator uses it to choose between a live load and a reboot, and
// init uses it to check that a live load changes nothing that needs a
// reboot. If the two compared differently, the operator could request
// a live load that init always refuses.

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// StorageDrift returns one line for each difference between the
// declared storage and the boot record's storage, role by role. Sizes
// are compared as byte counts, so 2048Mi and 2Gi are the same. The
// lines appear verbatim in condition messages.
func StorageDrift(desired, booted StorageSpec) []string {
	var diffs []string
	desiredRoles := rolesByName(desired)
	bootedRoles := rolesByName(booted)
	for _, name := range StorageRoleNames {
		d, dok := desiredRoles[name]
		a, aok := bootedRoles[name]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("%s: declared but this boot ran without it", name))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("%s: no longer declared but this boot ran with it", name))
		case dok && aok:
			if d.Device != a.Device {
				diffs = append(diffs, fmt.Sprintf("%s: device %s declared, %s in this boot", name, d.Device, a.Device))
			}
			if !sameSize(d.Size, a.Size) {
				diffs = append(diffs, fmt.Sprintf("%s: size %s declared, %s in this boot", name, orRemainder(d.Size), orRemainder(a.Size)))
			}
		}
	}
	return diffs
}

// NetworkDrift returns one line for each difference between the
// declared network and the boot record's network.
//
// A nil booted spec means the boot recorded no network, and
// NetworkDrift returns no differences. If it compared against an
// empty spec, every declared interface would be drift, and every
// machine with no network in its boot record would request a reboot.
//
// spec.network.hostEntries is not compared, because machine-operator
// writes /etc/hosts on every reconcile pass and an edit needs no
// reboot.
func NetworkDrift(desired NetworkSpec, booted *NetworkSpec) []string {
	if booted == nil {
		return nil
	}
	var diffs []string
	for i := range max(len(desired.Interfaces), len(booted.Interfaces)) {
		switch {
		case i >= len(booted.Interfaces):
			diffs = append(diffs, fmt.Sprintf("network: %s declared but this boot ran without it", desired.Interfaces[i].Name))
		case i >= len(desired.Interfaces):
			diffs = append(diffs, fmt.Sprintf("network: %s no longer declared but this boot ran with it", booted.Interfaces[i].Name))
		default:
			diffs = append(diffs, interfaceDrift(i, desired.Interfaces[i], booted.Interfaces[i])...)
		}
	}
	return diffs
}

// interfaceDrift compares the interfaces at one position of the two
// lists. When the names differ, it reports only the name, because the
// other fields then describe two different ports.
func interfaceDrift(position int, desired, booted InterfaceSpec) []string {
	if desired.Name != booted.Name {
		return []string{fmt.Sprintf("network: interface %d: %s declared, %s in this boot",
			position+1, desired.Name, booted.Name)}
	}
	var diffs []string
	if desired.Address != booted.Address {
		diffs = append(diffs, fmt.Sprintf("network: %s: address %s declared, %s in this boot",
			desired.Name, orDHCP(desired.Address), orDHCP(booted.Address)))
	}
	if desired.Gateway != booted.Gateway {
		diffs = append(diffs, fmt.Sprintf("network: %s: gateway %s declared, %s in this boot",
			desired.Name, orNone(desired.Gateway), orNone(booted.Gateway)))
	}
	if !slices.Equal(desired.Nameservers, booted.Nameservers) {
		diffs = append(diffs, fmt.Sprintf("network: %s: nameservers %s declared, %s in this boot",
			desired.Name,
			orNone(strings.Join(desired.Nameservers, ", ")),
			orNone(strings.Join(booted.Nameservers, ", "))))
	}
	if wirelessSummary(desired.Wireless) != wirelessSummary(booted.Wireless) {
		diffs = append(diffs, fmt.Sprintf("network: %s: wireless %s declared, %s in this boot",
			desired.Name,
			orNone(wirelessSummary(desired.Wireless)),
			orNone(wirelessSummary(booted.Wireless))))
	}
	return diffs
}

// wirelessSummary returns the SSID and security of a wireless entry,
// the fields that decide whether the machine must join the network
// again. It applies the security default first, so a spec that leaves
// the field unset matches a boot record that has the default.
func wirelessSummary(w *WirelessSpec) string {
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%s (%s)", w.SSID, w.SecurityOrDefault())
}

// RlimitDrift returns one line for each difference between the
// declared spec.rlimits and the boot's copy of it.
//
// OSRlimits is not compared, because a change to it ships in a new
// release, and the upgrade to that release reboots the machine.
//
// Values are compared as strings, so "1048576" and "1048576:1048576"
// are drift although they set the same limits. A person who rewrites
// a value in another form causes one reboot that changes nothing, and
// in return the comparison that decides a reboot does no parsing.
func RlimitDrift(desired, booted map[string]string) []string {
	var diffs []string
	names := map[string]bool{}
	for name := range desired {
		names[name] = true
	}
	for name := range booted {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		d, dok := desired[name]
		a, aok := booted[name]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s declared but this boot ran without it", name, d))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s no longer declared but this boot ran with it", name, a))
		case d != a:
			diffs = append(diffs, fmt.Sprintf("rlimit %s: %s declared, %s in this boot", name, d, a))
		}
	}
	return diffs
}

// ModuleSetDiff compares two module lists as sets, ignoring order and
// repeats, and returns the added and the retracted modules
// separately. init can load an added module into the running kernel.
// A retracted module stays loaded until the next boot, because the
// kernel cannot safely unload a driver that something may be using.
func ModuleSetDiff(desired, booted []string) (added, retracted []string) {
	want := map[string]bool{}
	for _, name := range desired {
		want[name] = true
	}
	have := map[string]bool{}
	for _, name := range booted {
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

// ModulesDrift returns one line for each added or retracted module and
// each changed module parameter. The booted list is the boot
// record's copy of the declared list, not the modules that loaded. A
// declared module that the image lacks is still in the booted list,
// because another reboot with the same image would not load it. The
// ModulesLoaded condition reports that module.
//
// decideConvergence in machine-operator/converge.go requests a live
// load only when the drift has one line for each added module or
// serio entry and no other lines. So a parameter line here makes a
// changed parameter need a reboot.
func ModulesDrift(desired, booted []string, desiredParameters, bootedParameters map[string]string) []string {
	added, retracted := ModuleSetDiff(desired, booted)
	var diffs []string
	for _, name := range added {
		diffs = append(diffs, fmt.Sprintf("modules: %s declared but this boot ran without it", name))
	}
	for _, name := range retracted {
		diffs = append(diffs, fmt.Sprintf("modules: %s no longer declared but this boot ran with it", name))
	}
	return append(diffs, ModuleParameterDrift(desired, booted, desiredParameters, bootedParameters)...)
}

// ModuleOrderDrift returns one line when the declared order of the
// modules differs from the order the boot loaded them in.
//
// Order decides which driver binds the hardware. When a sound
// controller's driver loads before the codec's own driver, the kernel
// binds the codec to the generic driver and the codec's outputs do
// not appear. Only a boot can load the list in a new order, so
// machine-operator stages a reordered spec for the next boot and
// requests no reboot.
//
// It compares only the modules that are in both lists, so an added or
// retracted module is not a reorder, and it reports the first
// position where the lists differ.
func ModuleOrderDrift(desired, booted []string) []string {
	shared := sharedModules(desired, booted)
	declared := sharedOrder(desired, shared)
	loaded := sharedOrder(booted, shared)
	for i := range declared {
		if declared[i] != loaded[i] {
			return []string{fmt.Sprintf("modules: the declared order changed: %s now loads before %s", declared[i], loaded[i])}
		}
	}
	return nil
}

// sharedOrder returns the names that are in shared, in list order and
// without repeats.
func sharedOrder(names []string, shared map[string]bool) []string {
	var order []string
	for _, name := range names {
		if shared[name] && !slices.Contains(order, name) {
			order = append(order, name)
		}
	}
	return order
}

// ModuleParameterDrift returns one line for each changed parameter of
// a module that is in both the desired and the booted lists.
//
// It skips the parameters of an added module, because init passes
// them when it loads the module. A loaded module does not read its
// parameters again, so a changed parameter of a loaded module needs a
// reboot.
func ModuleParameterDrift(desiredModules, bootedModules []string, desired, booted map[string]string) []string {
	shared := sharedModules(desiredModules, bootedModules)
	keys := map[string]bool{}
	for key := range desired {
		keys[key] = true
	}
	for key := range booted {
		keys[key] = true
	}
	var diffs []string
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		module, _, ok := splitModuleParameterKey(key)
		if !ok || !shared[module] {
			continue
		}
		d, dok := desired[key]
		a, aok := booted[key]
		switch {
		case dok && !aok:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s declared but this boot ran without it", key, d))
		case !dok && aok:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s no longer declared but this boot ran with it", key, a))
		case d != a:
			diffs = append(diffs, fmt.Sprintf("module parameter %s: %s declared, %s in this boot", key, d, a))
		}
	}
	return diffs
}

// sharedModules returns the modules that are in both lists.
func sharedModules(desired, booted []string) map[string]bool {
	have := map[string]bool{}
	for _, name := range booted {
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

// sameSize compares two sizes as byte counts. When either size does
// not parse, it compares the strings. Validation rejects such a size,
// so the result only has to be stable.
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

// orDHCP shows an empty address as "(DHCP)", because an interface
// with no address uses DHCP.
func orDHCP(address string) string {
	if address == "" {
		return "(DHCP)"
	}
	return address
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}

// SerioSetDiff compares two spec.serio lists as sets of whole
// entries, and returns the added and the retracted entries
// separately. init attaches an added entry while the machine runs. A
// retracted entry stays attached until the next boot, because a pod
// can hold the devices that the attachment created.
func SerioSetDiff(desired, booted []SerioAttachment) (added, retracted []SerioAttachment) {
	for _, a := range desired {
		if !slices.Contains(booted, a) && !slices.Contains(added, a) {
			added = append(added, a)
		}
	}
	for _, a := range booted {
		if !slices.Contains(desired, a) && !slices.Contains(retracted, a) {
			retracted = append(retracted, a)
		}
	}
	return added, retracted
}

// SerioDrift returns one line for each added or retracted spec.serio
// entry. decideConvergence counts these lines, as ModulesDrift
// describes.
func SerioDrift(desired, booted []SerioAttachment) []string {
	added, retracted := SerioSetDiff(desired, booted)
	var diffs []string
	for _, a := range added {
		diffs = append(diffs, fmt.Sprintf("serio: %s declared but this boot ran without it", a))
	}
	for _, a := range retracted {
		diffs = append(diffs, fmt.Sprintf("serio: %s no longer declared but this boot ran with it", a))
	}
	return diffs
}
