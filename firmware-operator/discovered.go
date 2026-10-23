package main

// The operator keeps one Firmware for each group of devices that a scan
// reports as updatable: a catalog component, or a set of GUIDs for a
// device with no release. grouping.go chooses the group. The object
// carries the label firmware.liken.sh/discovered, its spec.update names
// the group, and its status.machines holds one entry for each machine's
// device in the group, with the releases each catalog offers.
//
// A scan replaces its machine's entries for its FirmwareCatalog in every
// discovered object. An entry leaves only when that machine's next scan
// does not report the device, or when its Machine is deleted, so a
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
// against one FirmwareCatalog's file. DeviceID is fwupd's ID of the
// device, which keeps the entry in its component's object once the
// device runs the newest release.
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

// newest answers the release fwupd offers first, which is its newest,
// or "" when it offers none.
func (e machineEntry) newest() string {
	if len(e.Releases) == 0 {
		return ""
	}
	return e.Releases[0].Version
}

// discovered reports whether a Firmware is one the operator keeps.
func discovered(f *firmware) bool {
	return f.Spec.Update != nil && f.Metadata.Labels[discoveredLabel] == discoveredLabelValue && f.Metadata.DeletionTimestamp == ""
}

// pristine reports whether a person has left a discovered object's
// spec as the operator created it. The operator writes the spec only in
// the create, so any edit of the spec moves the generation past 1.
func pristine(f *firmware) bool {
	return f.Metadata.Generation == 1
}

// retirable reports whether the operator deletes a discovered object
// that is left with no entry. A pristine object goes. So does an object
// whose only edit is an approval: its component is still the one its
// name came from, and it names no GUIDs. With no entry, no machine is
// offered the approved release, so the approval installs nothing. A
// person who changed the group keeps the object. An apply that waits
// for its turn or writes keeps it too, because the operator still has
// to release that machine's hold.
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
// a group that has none, and deletes an object that is left with no
// entry. objects holds every Firmware the pass read, and gains the ones
// the operator creates.
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

// refileEntries moves the file of each machine's entries for a
// FirmwareCatalog to the file of the machine's Scanned record. The
// record moves to a new catalog file with no scan when the new file
// offers the same releases for the machine's GUIDs (scans.go), and the
// entries must name that file too: a rehearsal and an apply load the
// file an entry names, and start only when it is the FirmwareCatalog's
// current one. A scan writes its entries with its record's file, so
// this changes only the entries of a record that moved. It reads the
// records rather than the moves of this pass, so an operator that
// stopped between the two writes moves the entries in its next pass.
func (o *operator) refileEntries(objects []firmware, c *catalog) error {
	files := map[string]string{}
	for _, r := range c.Status.Machines {
		if r.Result == resultScanned {
			files[r.Machine] = r.File
		}
	}
	behind := func(e machineEntry) bool {
		return e.Catalog == c.Metadata.Name && files[e.Machine] != "" && e.File != files[e.Machine]
	}
	var problems []error
	for index := range objects {
		f := &objects[index]
		if !discovered(f) || !slices.ContainsFunc(f.Status.Machines, behind) {
			continue
		}
		problems = append(problems, o.settleEntries(f, func(held []machineEntry) []machineEntry {
			next := slices.Clone(held)
			for i := range next {
				if behind(next[i]) {
					next[i].File = files[next[i].Machine]
				}
			}
			return next
		}))
	}
	return errors.Join(problems...)
}

// replace answers the change that swaps the scan's machine's entries
// for its FirmwareCatalog in an object for entries.
func (s scanEntries) replace(entries []machineEntry) func([]machineEntry) []machineEntry {
	return func(held []machineEntry) []machineEntry {
		kept := slices.DeleteFunc(slices.Clone(held), func(e machineEntry) bool {
			return e.Machine == s.machine && e.Catalog == s.catalog
		})
		return append(kept, entries...)
	}
}

// forgetMachines removes the entries of every machine whose Machine is
// deleted, from every discovered object.
func (o *operator) forgetMachines(objects []firmware, fleet map[string]machine) error {
	var problems []error
	for index := range objects {
		f := &objects[index]
		if !discovered(f) {
			continue
		}
		problems = append(problems, o.settleEntries(f, func(held []machineEntry) []machineEntry {
			return slices.DeleteFunc(slices.Clone(held), func(e machineEntry) bool {
				_, exists := fleet[e.Machine]
				return !exists
			})
		}))
	}
	return errors.Join(problems...)
}

// createDiscovered creates the object of a group under the first of
// its names that no other group's object holds. An object that already
// exists under a name answers 409, and the operator reads it: the
// group's own object, such as one a create in an earlier pass made
// before the watch showed it, ends the search, and any other object
// moves it to the next name. A set of GUIDs' object whose GUIDs a
// person changed also ends the search, and keeps the entries of the
// device its name was made from. A component's object never takes
// another component's entries, because an approval of plan 04 names
// one component.
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
			var answer firmware
			err := o.client.RequestJSON(http.MethodPost, firmwaresPath, mustJSON(f), &answer)
			if errors.Is(err, apiclient.ErrConflict) {
				created = false
				return apiclient.Get[firmware](o.client, firmwarePath(name))
			}
			return &answer, err
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
	return nil, fmt.Errorf("creating the Firmware of %s: other Firmware objects hold the names %s", d.Name, strings.Join(names, " and "))
}

// ownedBy reports whether an object under one of a group's names holds
// the group's entries.
func ownedBy(f *firmware, key groupKey) bool {
	if !discovered(f) {
		return false
	}
	held := keyOf(f.Spec.Update)
	return held == key || (key.component == "" && held.component == "" && !pristine(f))
}

// settleEntries writes the entries that change gives a discovered
// object, and deletes a retirable object that is left with none. change
// runs again on the API server's copy after a 409.
func (o *operator) settleEntries(f *firmware, change func([]machineEntry) []machineEntry) error {
	var notices []notice
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(held *firmware) bool {
		before := held.Status
		next := held.Status
		next.Machines = change(held.Status.Machines)
		sortEntries(next.Machines)
		next.Counts = counts{UpdateCounts: updateCounts(next.Machines, held.Status.Counts.pending())}
		if len(next.Machines) == 0 {
			next.Machines = nil
		}
		notices = offers(before.Machines, next.Machines)
		if !statusChanged(before, next) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = firmwareAPIVersion, firmwareKind, next
		return true
	})
	// An object deleted during the pass needs no entries.
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
	// The Event outlives the object by the hour the API server keeps
	// it, so a person who finds an approval gone can read why.
	o.recorder.Normal(f.reference(), reasonDeleted, deletedMessage(f))
	return nil
}

// deletedMessage says why the operator deleted a discovered object.
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

// updateCounts answers an update rule's counts: the machines the
// entries name, the machines with at least one release offered, and
// the machines an approval waits for, which approval.go counts. The
// counts are new, so a change to them never writes through to the
// status that the API server holds.
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

// pending answers the count of machines an approval waits for, or 0
// when the status holds no update counts.
func (c counts) pending() int {
	if c.UpdateCounts == nil {
		return 0
	}
	return c.UpdateCounts.Pending
}

// offers answers an UpdateAvailable notice for each entry whose newest
// offered release changed, against the entry of the same machine,
// catalog, and device before.
func offers(before, after []machineEntry) []notice {
	held := map[string]string{}
	for _, e := range before {
		held[e.Machine+"\x00"+e.Catalog+"\x00"+e.Device] = e.newest()
	}
	var out []notice
	for _, e := range after {
		newest := e.newest()
		if newest == "" || held[e.Machine+"\x00"+e.Catalog+"\x00"+e.Device] == newest {
			continue
		}
		out = append(out, notice{reason: reasonUpdateAvailable, message: fmt.Sprintf(
			"%s on %s: %s offers %s, and the device runs %s", e.Device, e.Machine, e.Catalog, newest, e.Version)})
	}
	return out
}
