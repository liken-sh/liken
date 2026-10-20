package main

// A discovered Firmware groups the devices that one approval covers.
// A person approves a version of one catalog component, such as
// org.linuxfoundation.dbx.x64.firmware, so a device that a catalog
// offers a release for is an entry in the object of the release's
// component. A device's set of GUIDs does not name its kind: fwupd adds
// a GUID to the dbx device for each Key Exchange Key a machine has
// enrolled, and a USB device's first GUID holds its revision, which an
// update changes.
//
// A device with no release has nothing to approve, and it is an entry
// in an object for its set of GUIDs. An entry moves only toward a
// component: a device that installs the newest release gets no offer,
// and it stays in its component's object, which the operator finds by
// the fwupd DeviceId that the entry records. The entries are in status,
// so the rule holds across a restart of the operator.

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

// groupKey names the object that a device's entry belongs in: a
// catalog component, or a set of GUIDs when the device has no release.
type groupKey struct {
	component string
	guids     string
}

func keyOf(u *update) groupKey {
	if u.Component != "" {
		return groupKey{component: u.Component}
	}
	return groupKey{guids: modelKey(u.GUIDs)}
}

// scanEntries groups the entries of one scan by the object each one
// belongs in.
type scanEntries struct {
	machine, catalog string
	byGroup          map[groupKey][]machineEntry
	// devices holds the first device of each group, for an object the
	// operator creates.
	devices map[groupKey]scanDevice
	// warnings holds the ManyComponents notices of each group.
	warnings map[groupKey][]notice
}

// entriesOf groups a scan's devices. objects holds every Firmware the
// pass read, where the operator finds the component that already holds
// a device's entry.
func entriesOf(record machineRecord, catalog string, devices []scanDevice, objects []firmware) scanEntries {
	s := scanEntries{machine: record.Machine, catalog: catalog,
		byGroup: map[groupKey][]machineEntry{}, devices: map[groupKey]scanDevice{}, warnings: map[groupKey][]notice{}}
	held := heldComponents(objects, record.Machine, catalog)
	for _, d := range devices {
		if modelKey(d.GUIDs) == "" {
			continue
		}
		key, others := groupOf(d, held)
		s.byGroup[key] = append(s.byGroup[key], machineEntry{
			Machine: record.Machine, Device: d.Name, DeviceID: d.DeviceID, Version: d.Version, Catalog: catalog,
			File: record.File, Releases: d.Releases, Time: record.Time,
		})
		if _, held := s.devices[key]; !held {
			s.devices[key] = d
		}
		if len(others) > 0 {
			s.warnings[key] = append(s.warnings[key], notice{warning: true, reason: reasonManyComponents, message: fmt.Sprintf(
				"%s on %s: %s offers releases of %s too, and the device is an entry of %s, the component of the newest release",
				d.Name, record.Machine, catalog, strings.Join(others, ", "), key.component)})
		}
	}
	return s
}

// groupOf answers the group of a device, and the components other than
// the chosen one that its releases come from. fwupd lists the newest
// release first.
func groupOf(d scanDevice, held map[string]string) (groupKey, []string) {
	if len(d.Releases) > 0 && d.Releases[0].ID != "" {
		chosen := d.Releases[0].ID
		var others []string
		for _, r := range d.Releases[1:] {
			if r.ID != "" && r.ID != chosen && !slices.Contains(others, r.ID) {
				others = append(others, r.ID)
			}
		}
		return groupKey{component: chosen}, others
	}
	if component := held[d.DeviceID]; d.DeviceID != "" && component != "" {
		return groupKey{component: component}, nil
	}
	return groupKey{guids: modelKey(d.GUIDs)}, nil
}

// heldComponents answers the component of each fwupd DeviceId that a
// component's object holds an entry for, for one machine and catalog.
func heldComponents(objects []firmware, machine, catalog string) map[string]string {
	held := map[string]string{}
	for index := range objects {
		f := &objects[index]
		if !discovered(f) || f.Spec.Update.Component == "" {
			continue
		}
		for _, e := range f.Status.Machines {
			if e.Machine == machine && e.Catalog == catalog && e.DeviceID != "" {
				held[e.DeviceID] = f.Spec.Update.Component
			}
		}
	}
	return held
}

// sortedGroups answers the groups in a fixed order, so the operator
// creates objects in the same order on every run.
func sortedGroups[T any](groups map[groupKey]T) []groupKey {
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b groupKey) int {
		return strings.Compare(a.component+"\x00"+a.guids, b.component+"\x00"+b.guids)
	})
	return keys
}

// modelKey names a device model by its GUIDs.
func modelKey(guids []string) string {
	return strings.Join(sortedSet(guids), ",")
}

// groupNames answers the names of a group's object, in the order the
// operator tries them. A component's object takes the component ID
// with no top-level domain and no "firmware" part, such as
// microsoft-dbx-x64, because two IDs of one catalog give one such name
// only when they differ in case alone, and then the second takes the
// whole ID in lowercase. A set of GUIDs' object always takes the
// device's name and a hash of the GUIDs, such as
// uefi-device-firmware-042452fb, because many devices share a name:
// fwupd names each ESRT entry UEFI Device Firmware. So a device gets
// the same name on every cluster, whichever device the operator meets
// first.
func groupNames(key groupKey, d scanDevice) []string {
	if key.component != "" {
		return []string{shortComponentName(key.component), componentName(key.component)}
	}
	return []string{discoveredName(d.Name, d.GUIDs)}
}

// shortComponentName answers a component ID with no top-level domain
// and no last part "firmware", as one DNS label.
func shortComponentName(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) > 1 && strings.EqualFold(parts[len(parts)-1], "firmware") {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 1 {
		parts = parts[1:]
	}
	name := slug(strings.Join(parts, "-"))
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if name == "" {
		return "component"
	}
	return name
}

// componentName answers the name of a component's object: the
// component ID in lowercase, with each part between the dots made a DNS
// label, so the name reads as the ID does.
func componentName(id string) string {
	var labels []string
	for _, part := range strings.Split(id, ".") {
		label := slug(part)
		if len(label) > 63 {
			label = strings.TrimRight(label[:63], "-")
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	name := strings.Join(labels, ".")
	if len(name) > 253 {
		name = strings.TrimRight(name[:253], ".-")
	}
	if name == "" {
		return "component"
	}
	return name
}

// discoveredName answers the name of a set of GUIDs' object: the
// device's name as a DNS label, and eight hex digits of a hash of the
// GUIDs, so two sets with one device name get two objects.
func discoveredName(device string, guids []string) string {
	sum := sha256.Sum256([]byte(modelKey(guids)))
	suffix := "-" + shortHex(sum[:])[:8]
	prefix := slug(device)
	if len(prefix) > 63-len(suffix) {
		prefix = strings.TrimRight(prefix[:63-len(suffix)], "-")
	}
	if prefix == "" {
		prefix = "device"
	}
	return prefix + suffix
}

// slug answers text in lowercase with each run of other characters than
// letters and digits as one -, and no - at either end.
func slug(text string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case !strings.HasSuffix(out.String(), "-"):
			out.WriteRune('-')
		}
	}
	return strings.Trim(out.String(), "-")
}
