package main

// A discovered Firmware object holds the entries of all the devices
// that one approval applies to. A person approves a version of one
// catalog component, such as org.linuxfoundation.dbx.x64.firmware, so a
// device that the catalog offers a release for gets its entry in the
// object of that release's component. The device's GUIDs cannot group
// devices this way: fwupd adds a GUID to the dbx device for each Key
// Exchange Key that a machine has enrolled, and the first GUID of a USB
// device includes its revision, which an update changes.
//
// A device with no release has nothing to approve, so its entry goes in
// an object for its set of GUIDs. A device whose entry is already in a
// component's object stays there after it installs the newest release
// and the catalog stops offering it one, because the operator finds the
// component from the fwupd DeviceId in the existing entry. The entries
// are in status, so this works across an operator restart.

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

// groupKey identifies the object that a device's entry belongs in. It
// is a catalog component, or a set of GUIDs when the device has no
// release.
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

// scanEntries holds the entries of one scan, grouped by the object that
// each belongs in.
type scanEntries struct {
	machine, catalog string
	byGroup          map[groupKey][]machineEntry
	// devices holds the first device of each group, which the operator
	// uses to name and create the group's object.
	devices map[groupKey]scanDevice
	// warnings holds each group's ManyComponents notices.
	warnings map[groupKey][]notice
}

// entriesOf groups a scan's devices, and skips a device with no GUIDs.
// objects is every Firmware that the pass read, which groupOf uses to
// find the component object that already has a device's entry.
func entriesOf(record machineRecord, catalog string, devices []scanDevice, objects []firmware) scanEntries {
	s := scanEntries{machine: record.Machine, catalog: catalog,
		byGroup: map[groupKey][]machineEntry{}, devices: map[groupKey]scanDevice{}, warnings: map[groupKey][]notice{}}
	componentOf := componentsByDeviceID(objects, record.Machine, catalog)
	for _, d := range devices {
		if modelKey(d.GUIDs) == "" {
			continue
		}
		key, others := groupOf(d, componentOf)
		s.byGroup[key] = append(s.byGroup[key], machineEntry{
			Machine: record.Machine, Device: d.Name, DeviceID: d.DeviceID, Version: d.Version, Catalog: catalog,
			File: record.File, Releases: d.Releases, Time: record.Time,
		})
		if _, seen := s.devices[key]; !seen {
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

// groupOf returns the group of a device, and the other components that
// its releases come from. The group is the component of the first
// release, because fwupd lists the newest release first. When the
// device has no release, the group is the component of an object that
// already has an entry for its DeviceId, or else its set of GUIDs.
func groupOf(d scanDevice, componentOf map[string]string) (groupKey, []string) {
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
	if component := componentOf[d.DeviceID]; d.DeviceID != "" && component != "" {
		return groupKey{component: component}, nil
	}
	return groupKey{guids: modelKey(d.GUIDs)}, nil
}

// componentsByDeviceID returns, for one machine and catalog, the
// component of each fwupd DeviceId that a component's object already
// has an entry for.
func componentsByDeviceID(objects []firmware, machine, catalog string) map[string]string {
	componentOf := map[string]string{}
	for index := range objects {
		f := &objects[index]
		if !discovered(f) || f.Spec.Update.Component == "" {
			continue
		}
		for _, e := range f.Status.Machines {
			if e.Machine == machine && e.Catalog == catalog && e.DeviceID != "" {
				componentOf[e.DeviceID] = f.Spec.Update.Component
			}
		}
	}
	return componentOf
}

// sortedGroups returns the keys of groups in a fixed order, so the
// operator creates objects in the same order on every run.
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

// modelKey returns the sorted, comma-separated GUIDs of a device. It
// identifies the device's model.
func modelKey(guids []string) string {
	return strings.Join(sortedSet(guids), ",")
}

// groupNames returns the candidate names for a group's object, in the
// order the operator tries them.
//
// A component's object is named from the component ID without its
// top-level domain and its "firmware" part, such as microsoft-dbx-x64.
// When another component's object already has that name, the operator
// tries the whole ID in lowercase.
//
// An object for a set of GUIDs is always named from the device's name
// and a hash of the GUIDs, such as uefi-device-firmware-042452fb,
// because many devices share a name (fwupd names every ESRT entry UEFI
// Device Firmware). The name depends only on the device, so it is the
// same on every cluster, whichever device the operator reads first.
func groupNames(key groupKey, d scanDevice) []string {
	if key.component != "" {
		return []string{shortComponentName(key.component), componentName(key.component)}
	}
	return []string{discoveredName(d.Name, d.GUIDs)}
}

// shortComponentName returns a component ID as one DNS label, without
// its first part (the top-level domain) and without a last part of
// "firmware".
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

// componentName returns the whole component ID in lowercase, with each
// dot-separated part converted to a DNS label, so the name reads like
// the ID.
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

// discoveredName returns the name of the object for a set of GUIDs:
// the device's name as a DNS label followed by eight hex digits of a
// hash of the GUIDs, so two sets with the same device name get two
// objects.
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

// slug returns text in lowercase, with each run of characters that are
// not letters or digits replaced by one "-" and no "-" at either end.
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
