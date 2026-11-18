package main

// The operator creates one Firmware for each group of devices that a
// scan reports as updatable. A group is a catalog component, or a set
// of GUIDs for a device that no catalog offers a release. grouping.go
// chooses the group. The object has the label
// firmware.liken.sh/discovered, its spec.update names the group, and
// its status.machines has one entry for each machine's device in the
// group, with the releases that each catalog offers.
//
// A scan replaces its machine's entries for its FirmwareCatalog in every
// discovered object. An entry is removed only when that machine's next
// scan does not report the device, or when its Machine is deleted. A
// machine that is off keeps its entries.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

const (
	discoveredLabel      = "firmware.liken.sh/discovered"
	discoveredLabelValue = "true"
)

// machineEntry is one machine's device in a discovered group, matched
// against one FirmwareCatalog's file. DeviceID is fwupd's ID for the
// device. grouping.go finds the component object of a device by its
// DeviceID, so the entry stays in that object after the device installs
// the newest release and the catalog stops offering it one.
type machineEntry struct {
	Machine  string           `json:"machine"`
	Device   string           `json:"device"`
	DeviceID string           `json:"deviceId,omitempty"`
	Version  string           `json:"version,omitempty"`
	Catalog  string           `json:"catalog"`
	File     string           `json:"file"`
	Releases []offeredRelease `json:"releases,omitempty"`
	Time     string           `json:"time"`
}

// newest returns the first release that fwupd offers, which is the
// newest, or "" when it offers none.
func (e machineEntry) newest() string {
	if len(e.Releases) == 0 {
		return ""
	}
	return e.Releases[0].Version
}

// discovered reports whether the operator created f for a group and f
// is not being deleted.
func discovered(f *firmware) bool {
	return f.Spec.Update != nil && f.Metadata.Labels[discoveredLabel] == discoveredLabelValue && f.Metadata.DeletionTimestamp == ""
}

// pristine reports whether nobody has edited a discovered object's spec.
// The operator writes the spec only when it creates the object, so any
// edit of the spec raises the generation above 1.
func pristine(f *firmware) bool {
	return f.Metadata.Generation == 1
}

// retirable reports whether the operator may delete a discovered object
// that has no entries. That is true for a pristine object. It is also
// true for an object whose only edit is an approval: spec.update names
// no GUIDs and the component that the object's name was made from. With
// no entries, no machine is offered the approved release, so the
// approval installs nothing. It is false while an apply waits for its
// turn or writes, because the operator still has to release that
// machine's hold.
func retirable(f *firmware) bool {
	if pristine(f) {
		return true
	}
	u := f.Spec.Update
	if u == nil || u.Component == "" || len(u.GUIDs) > 0 ||
		!slices.Contains(groupNames(groupKey{component: u.Component}, scanDevice{}), f.Metadata.Name) {
		return false
	}
	return !slices.ContainsFunc(f.Status.Applies, func(r applyRecord) bool {
		return r.Result == resultWaiting || r.Result == resultWriting
	})
}

// applyScan replaces a machine's entries for a FirmwareCatalog in every
// discovered object with what its scan reported, creates an object for
// each reported group that has none, and deletes an object that has no
// entries left. objects is every Firmware that the pass read, and
// applyScan appends each object it creates.
func (o *operator) applyScan(objects *[]firmware, s scanEntries) error {
	var problems []error
	present := map[groupKey]bool{}
	for index := range *objects {
		f := &(*objects)[index]
		if !discovered(f) {
			continue
		}
		key := keyOf(f.Spec.Update)
		present[key] = true
		problems = append(problems, o.settleEntries(f, s.replace(s.byGroup[key])))
		o.post(f, condition{}, s.warnings[key], false)
	}
	for _, key := range sortedGroups(s.byGroup) {
		if present[key] {
			continue
		}
		f, err := o.createDiscovered(key, s.devices[key])
		if err != nil {
			problems = append(problems, err)
			continue
		}
		*objects = append(*objects, *f)
		created := &(*objects)[len(*objects)-1]
		problems = append(problems, o.settleEntries(created, s.replace(s.byGroup[key])))
		o.post(created, condition{}, s.warnings[key], false)
	}
	return errors.Join(problems...)
}

// moveEntriesToScannedFile sets the File of each machine's entries for
// catalog c to the file in that machine's Scanned record. scans.go
// moves a record to the catalog's new file without a scan when the new
// file offers the same releases for the machine's GUIDs. A rehearsal
// and an apply load the file that an entry names, and start only when
// that is the catalog's current file, so the entries must follow the
// record. moveEntriesToScannedFile reads the records instead of the
// moves of this pass, so when the operator stops between the record
// write and the entry write, the next pass still updates the entries.
func (o *operator) moveEntriesToScannedFile(objects []firmware, c *catalog) error {
	files := map[string]string{}
	for _, r := range c.Status.Machines {
		if r.Result == resultScanned {
			files[r.Machine] = r.File
		}
	}
	stale := func(e machineEntry) bool {
		return e.Catalog == c.Metadata.Name && files[e.Machine] != "" && e.File != files[e.Machine]
	}
	var problems []error
	for index := range objects {
		f := &objects[index]
		if !discovered(f) || !slices.ContainsFunc(f.Status.Machines, stale) {
			continue
		}
		problems = append(problems, o.settleEntries(f, func(current []machineEntry) []machineEntry {
			next := slices.Clone(current)
			for i := range next {
				if stale(next[i]) {
					next[i].File = files[next[i].Machine]
				}
			}
			return next
		}))
	}
	return errors.Join(problems...)
}

// replace returns a change for settleEntries that replaces the scan's
// machine's entries for the scan's FirmwareCatalog with entries.
func (s scanEntries) replace(entries []machineEntry) func([]machineEntry) []machineEntry {
	return func(current []machineEntry) []machineEntry {
		others := slices.DeleteFunc(slices.Clone(current), func(e machineEntry) bool {
			return e.Machine == s.machine && e.Catalog == s.catalog
		})
		return append(others, entries...)
	}
}

// removeEntriesOfDeletedMachines removes, from every discovered object,
// the entries of each machine that has no Machine in fleet.
func (o *operator) removeEntriesOfDeletedMachines(objects []firmware, fleet map[string]machine) error {
	var problems []error
	for index := range objects {
		f := &objects[index]
		if !discovered(f) {
			continue
		}
		problems = append(problems, o.settleEntries(f, func(current []machineEntry) []machineEntry {
			return slices.DeleteFunc(slices.Clone(current), func(e machineEntry) bool {
				_, exists := fleet[e.Machine]
				return !exists
			})
		}))
	}
	return errors.Join(problems...)
}

// createDiscovered creates the object of a group under the first name in
// groupNames that another group's object does not use. When a name is
// taken, the create fails with 409 and the operator reads the existing
// object. It returns that object when ownedBy reports it as the group's
// own, which happens when an earlier pass created it before the watch
// reported it. Otherwise it tries the next name.
func (o *operator) createDiscovered(key groupKey, d scanDevice) (*firmware, error) {
	u := &update{Component: key.component}
	about := fmt.Sprintf("%s, the catalog component %s", d.Name, key.component)
	if key.component == "" {
		u = &update{GUIDs: sortedSet(d.GUIDs)}
		about = fmt.Sprintf("%s, with the GUIDs %s", d.Name, strings.Join(u.GUIDs, ", "))
	}
	names := groupNames(key, d)
	for _, name := range names {
		f := firmware{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Spec: firmwareSpec{Update: u}}
		f.Metadata.Name = name
		f.Metadata.Labels = map[string]string{discoveredLabel: discoveredLabelValue, "app.kubernetes.io/managed-by": component}
		created := true
		stored, err := memo.Written[firmware](o.firmwares.Versions, name, func() (*firmware, error) {
			var response firmware
			err := o.client.RequestJSON(http.MethodPost, firmwaresPath, mustJSON(f), &response)
			if errors.Is(err, apiclient.ErrConflict) {
				created = false
				return apiclient.Get[firmware](o.client, firmwarePath(name))
			}
			return &response, err
		})
		if err != nil {
			return nil, fmt.Errorf("creating the Firmware %s: %w", name, err)
		}
		if !ownedBy(stored, key) {
			continue
		}
		if created {
			o.recorder.Normal(stored.reference(), reasonDiscovered, about)
		}
		return stored, nil
	}
	return nil, fmt.Errorf("creating the Firmware of %s: the names %s belong to other Firmware objects", d.Name, strings.Join(names, " and "))
}

// ownedBy reports whether f, which has one of the group's names, is the
// object for the group's entries. A GUID object that a person edited
// counts as its group's own, so it keeps the entries of the device that
// its name was made from. A component's object never gets another
// component's entries, because an approval applies to one component.
func ownedBy(f *firmware, key groupKey) bool {
	if !discovered(f) {
		return false
	}
	existing := keyOf(f.Spec.Update)
	return existing == key || (key.component == "" && existing.component == "" && !pristine(f))
}

// settleEntries writes the entries that change returns for a discovered
// object, and deletes the object when it has no entries and is
// retirable. When the write fails because f is stale, change runs
// again on a fresh copy from the API server.
func (o *operator) settleEntries(f *firmware, change func([]machineEntry) []machineEntry) error {
	var notices []notice
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(latest *firmware) bool {
		before := latest.Status
		next := latest.Status
		next.Machines = change(latest.Status.Machines)
		sortEntries(next.Machines)
		next.Counts = counts{UpdateCounts: updateCounts(next.Machines, latest.Status.Counts.pending())}
		if len(next.Machines) == 0 {
			next.Machines = nil
		}
		notices = offers(before.Machines, next.Machines)
		if !statusChanged(before, next) {
			return false
		}
		latest.APIVersion, latest.Kind, latest.Status = firmwareAPIVersion, firmwareKind, next
		return true
	})
	// Another writer deleted the object during the pass.
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if written {
		o.post(f, condition{}, notices, false)
	}
	if len(f.Status.Machines) > 0 || !retirable(f) {
		return nil
	}
	err = o.client.RequestJSON(http.MethodDelete, firmwarePath(f.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// The API server keeps the Event for an hour after the delete, so a
	// person who finds an approval gone can read why.
	o.recorder.Normal(f.reference(), reasonDeleted, deletedMessage(f))
	return nil
}

// deletedMessage returns the message of the Event that the operator posts
// when it deletes a discovered object.
func deletedMessage(f *firmware) string {
	if pristine(f) {
		return "No machine reports a device that this object covers, so the operator deleted it."
	}
	return "No machine reports a device that this object covers, so its approval installs nothing. The operator deleted it."
}

func sortEntries(entries []machineEntry) {
	slices.SortStableFunc(entries, func(a, b machineEntry) int {
		return strings.Compare(a.Machine+"\x00"+a.Catalog+"\x00"+a.Device, b.Machine+"\x00"+b.Catalog+"\x00"+b.Device)
	})
}

// updateCounts returns the counts for an update rule: the machines in
// the entries, the machines with at least one release offered, and the
// machines that an approval waits for, which approval.go counts. It
// returns a new value because settleEntries compares the new status
// with the old one, and both start with the old UpdateCounts pointer.
func updateCounts(entries []machineEntry, pending int) *UpdateCounts {
	all, behind := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		all[e.Machine] = true
		if len(e.Releases) > 0 {
			behind[e.Machine] = true
		}
	}
	return &UpdateCounts{Machines: len(all), Behind: len(behind), Pending: pending}
}

// pending returns the count of machines that an approval waits for, or
// 0 when the status has no update counts.
func (c counts) pending() int {
	if c.UpdateCounts == nil {
		return 0
	}
	return c.UpdateCounts.Pending
}

// offers returns an UpdateAvailable notice for each entry in after whose
// newest offered release differs from the newest release of the entry
// in before for the same machine, catalog, and device.
func offers(before, after []machineEntry) []notice {
	previous := map[string]string{}
	for _, e := range before {
		previous[e.Machine+"\x00"+e.Catalog+"\x00"+e.Device] = e.newest()
	}
	var out []notice
	for _, e := range after {
		newest := e.newest()
		if newest == "" || previous[e.Machine+"\x00"+e.Catalog+"\x00"+e.Device] == newest {
			continue
		}
		out = append(out, notice{reason: reasonUpdateAvailable, message: fmt.Sprintf(
			"%s on %s: %s offers %s, and the device runs %s", e.Device, e.Machine, e.Catalog, newest, e.Version)})
	}
	return out
}
