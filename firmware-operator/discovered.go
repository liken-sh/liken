package main

// The operator keeps one Firmware for each firmware device model that a
// scan reports as updatable. A model is the sorted set of the device's
// GUIDs, because fwupd matches a release to a device by its GUIDs. The
// object's name is the device's name and a short hash of the GUIDs, and
// it carries the label firmware.liken.sh/discovered. Its spec.update
// holds the GUIDs, and its status.machines holds one entry for each
// machine's device of that model, with the releases each catalog
// offers.
//
// A scan replaces its machine's entries for its Catalog in every
// discovered object. An entry leaves only when that machine's next scan
// does not report the device, or when its Machine is deleted, so a
// machine that is off keeps its entries.

import (
	"crypto/sha256"
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

// machineEntry is one machine's device of a discovered model, matched
// against one Catalog's file.
type machineEntry struct {
	Machine  string           `json:"machine"`
	Device   string           `json:"device"`
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

// modelKey names a device model by its GUIDs.
func modelKey(guids []string) string {
	return strings.Join(sortedSet(guids), ",")
}

// discoveredName answers the name of a model's object: the device's
// name as a DNS label, and eight hex digits of a hash of the GUIDs, so
// two models with one device name get two objects.
func discoveredName(device string, guids []string) string {
	sum := sha256.Sum256([]byte(modelKey(guids)))
	suffix := "-" + shortHex(sum[:])[:8]
	var slug strings.Builder
	for _, r := range strings.ToLower(device) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
		case !strings.HasSuffix(slug.String(), "-"):
			slug.WriteRune('-')
		}
	}
	prefix := strings.Trim(slug.String(), "-")
	if len(prefix) > 63-len(suffix) {
		prefix = strings.TrimRight(prefix[:63-len(suffix)], "-")
	}
	if prefix == "" {
		prefix = "device"
	}
	return prefix + suffix
}

// discovered reports whether a Firmware is one the operator keeps.
func discovered(f *firmware) bool {
	return f.Spec.Update != nil && f.Metadata.Labels[discoveredLabel] == discoveredLabelValue && f.Metadata.DeletionTimestamp == ""
}

// pristine reports whether a person has left a discovered object's
// spec as the operator created it. The operator writes the spec only in
// the create, so any edit of the spec moves the generation past 1. Only
// a pristine object with no entry is deleted: a person who approved a
// version or changed the GUIDs keeps the object.
func pristine(f *firmware) bool {
	return f.Metadata.Generation == 1
}

// scanEntries groups the entries of one scan by device model.
type scanEntries struct {
	machine, catalog string
	byModel          map[string][]machineEntry
	// names holds the device's name and GUIDs of each model, for an
	// object the operator creates.
	names map[string]scanDevice
}

func entriesOf(record machineRecord, catalog string, devices []scanDevice) scanEntries {
	s := scanEntries{machine: record.Machine, catalog: catalog, byModel: map[string][]machineEntry{}, names: map[string]scanDevice{}}
	for _, d := range devices {
		key := modelKey(d.GUIDs)
		if key == "" {
			continue
		}
		s.byModel[key] = append(s.byModel[key], machineEntry{
			Machine: record.Machine, Device: d.Name, Version: d.Version, Catalog: catalog,
			File: record.File, Releases: d.Releases, Time: record.Time,
		})
		if _, held := s.names[key]; !held {
			s.names[key] = d
		}
	}
	return s
}

// applyScan replaces a machine's entries for a Catalog in every
// discovered object with what its scan reported, creates an object for
// a model that has none, and deletes an object that is left with no
// entry. objects holds every Firmware the pass read, and gains the ones
// the operator creates.
func (o *operator) applyScan(objects *[]firmware, s scanEntries) error {
	var problems []error
	models := map[string]bool{}
	for index := range *objects {
		f := &(*objects)[index]
		if !discovered(f) {
			continue
		}
		key := modelKey(f.Spec.Update.GUIDs)
		models[key] = true
		problems = append(problems, o.settleEntries(f, func(held []machineEntry) []machineEntry {
			kept := slices.DeleteFunc(slices.Clone(held), func(e machineEntry) bool {
				return e.Machine == s.machine && e.Catalog == s.catalog
			})
			return append(kept, s.byModel[key]...)
		}))
	}
	keys := make([]string, 0, len(s.byModel))
	for key := range s.byModel {
		if !models[key] {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		f, err := o.createDiscovered(s.names[key])
		if err != nil {
			problems = append(problems, err)
			continue
		}
		*objects = append(*objects, *f)
		entries := s.byModel[key]
		problems = append(problems, o.settleEntries(&(*objects)[len(*objects)-1], func(held []machineEntry) []machineEntry {
			return append(slices.DeleteFunc(slices.Clone(held), func(e machineEntry) bool {
				return e.Machine == s.machine && e.Catalog == s.catalog
			}), entries...)
		}))
	}
	return errors.Join(problems...)
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

// createDiscovered creates the object of a model. An object that
// already exists under the name, such as one a create in an earlier
// pass made before the watch showed it, answers 409, and the operator
// reads it.
func (o *operator) createDiscovered(d scanDevice) (*firmware, error) {
	name := discoveredName(d.Name, d.GUIDs)
	f := firmware{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Spec: firmwareSpec{Update: &update{GUIDs: sortedSet(d.GUIDs)}}}
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
	if created {
		o.recorder.Normal(stored.reference(), reasonDiscovered, fmt.Sprintf("%s, with the GUIDs %s", d.Name, strings.Join(f.Spec.Update.GUIDs, ", ")))
	}
	return stored, nil
}

// settleEntries writes the entries that change gives a discovered
// object, and deletes a pristine object that is left with none. change
// runs again on the API server's copy after a 409.
func (o *operator) settleEntries(f *firmware, change func([]machineEntry) []machineEntry) error {
	var notices []notice
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(held *firmware) bool {
		before := held.Status
		next := held.Status
		next.Machines = change(held.Status.Machines)
		sortEntries(next.Machines)
		next.Counts.Machines, next.Counts.Behind = machineCounts(next.Machines)
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
	if len(f.Status.Machines) > 0 || !pristine(f) {
		return nil
	}
	err = o.client.RequestJSON(http.MethodDelete, firmwarePath(f.Metadata.Name), nil, nil)
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

func sortEntries(entries []machineEntry) {
	slices.SortStableFunc(entries, func(a, b machineEntry) int {
		return strings.Compare(a.Machine+"\x00"+a.Catalog+"\x00"+a.Device, b.Machine+"\x00"+b.Catalog+"\x00"+b.Device)
	})
}

// machineCounts answers the number of machines the entries name, and
// the number of those with at least one release offered.
func machineCounts(entries []machineEntry) (int, int) {
	all, behind := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		all[e.Machine] = true
		if len(e.Releases) > 0 {
			behind[e.Machine] = true
		}
	}
	return len(all), len(behind)
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
