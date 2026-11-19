package main

// For each FirmwareCatalog, a pass writes the results of the finished
// scan pods to the machine records in the FirmwareCatalog's status and
// to the discovered Firmware objects. Then it reads the catalog file (a
// jcat) when a read is due, and starts a scan on each Ready machine that
// needs one.
//
// A machine needs a scan when its boot time, the hash of the device
// names in its ResourceSlices, the catalog file, or the scan image
// differs from its record. A device's version changes only at a reboot
// or a flash. The device names leave out the device number, which
// changes when a device is replugged. When only the catalog file
// changed, and the new file offers the same releases for the GUIDs of
// the machine's last scan, the operator moves the record and the
// machine's discovered entries to the new file and starts no scan
// (discovered.go). A failed scan runs again only after one of those
// values changes. A scan whose pod the kubelet ended for a reason other
// than a node shutdown did not fail, and runs again in a next pod
// (retry.go).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// discover reconciles every FirmwareCatalog and returns the earliest
// time at which a scan deadline or a catalog file read is due, or the
// zero time when none is.
func (o *operator) discover(ctx context.Context, rules *[]firmware, w observed, now time.Time) (time.Time, error) {
	catalogs, err := informer.List[catalog](o.client, o.catalogs, catalogsPath, catalogPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("listing the FirmwareCatalog objects: %w", err)
	}
	var next time.Time
	var problems []error
	present := map[string]bool{}
	byName := map[string]*catalog{}
	for index := range catalogs {
		c := &catalogs[index]
		present[c.Metadata.Name] = true
		byName[c.Metadata.Name] = c
		// Checking for shutdown between FirmwareCatalogs means a shutdown
		// waits for the writes of at most one FirmwareCatalog.
		if ctx.Err() != nil || c.Metadata.DeletionTimestamp != "" {
			continue
		}
		due, err := o.reconcileCatalog(ctx, c, rules, w, now)
		if err != nil {
			problems = append(problems, fmt.Errorf("reconciling the FirmwareCatalog %s: %w", c.Metadata.Name, err))
		}
		next = earliest(next, due)
	}
	o.deleteCachesOfDeletedCatalogs(present)
	if ctx.Err() == nil {
		problems = append(problems, o.removeEntriesOfDeletedMachines(*rules, w.fleet))
	}
	if ctx.Err() == nil {
		due, err := o.approveAll(*rules, w, byName, now)
		next = earliest(next, due)
		problems = append(problems, err)
	}
	return next, errors.Join(problems...)
}

func (o *operator) reconcileCatalog(ctx context.Context, c *catalog, rules *[]firmware, w observed, now time.Time) (time.Time, error) {
	ends, retries, busy, due, err := o.endsOf(c, w.scans[c.Metadata.Name], w, now)
	problems := []error{err}
	if err := o.recordEnds(ctx, c, ends, retries, rules, w, now); err != nil {
		return due, errors.Join(append(problems, err)...)
	}
	moved := slices.ContainsFunc(ends, func(end scanEnd) bool { return end.result == resultCatalogMoved })
	next, err := o.schedule(ctx, c, moved, busy, w, now)
	problems = append(problems, err, o.moveEntriesToScannedFile(*rules, c))
	return earliest(due, next), errors.Join(problems...)
}

// recordEnds writes the result of each finished scan to the
// FirmwareCatalog's status, and drops the record of each machine whose
// Machine is deleted. A scan whose pod the kubelet ended keeps its
// Running record, with a message that names the next pod. Then
// recordEnds writes the devices of each successful scan to the
// discovered Firmware objects, deletes the finished scan pods, and
// replaces each pod that the kubelet ended whose wait is over.
func (o *operator) recordEnds(ctx context.Context, c *catalog, ends []scanEnd, retries []retry, rules *[]firmware, w observed, now time.Time) error {
	releases := map[string]string{}
	for _, end := range ends {
		if end.result == resultScanned && end.attempt.file == c.Status.File {
			index, err := o.indexOf(ctx, c, c.Status.File, c.Status.SHA256)
			if err != nil {
				// The record gets no releases hash, so a change of
				// catalog file scans this machine again.
				fmt.Fprintf(os.Stderr, "firmware-operator: reading the catalog file of %s: %v\n", c.Metadata.Name, err)
				continue
			}
			releases[end.attempt.machine] = index.releasesHash(guidsOf(end.devices))
		}
	}
	var notices []notice
	written, err := o.settleCatalog(c, func(current catalogStatus) catalogStatus {
		notices = nil
		records := map[string]machineRecord{}
		for _, r := range current.Machines {
			if _, exists := w.fleet[r.Machine]; exists {
				records[r.Machine] = r
			}
		}
		for _, end := range ends {
			previous, found := records[end.attempt.machine]
			_, exists := w.fleet[end.attempt.machine]
			if !exists || (found && end.attempt.is(previous) && previous.Result != resultRunning) {
				continue
			}
			records[end.attempt.machine] = recordOf(end, previous, releases[end.attempt.machine], now)
			notices = append(notices, endNotice(end))
		}
		for _, r := range retries {
			a := scanOf(r.ended)
			previous, found := records[a.machine]
			_, exists := w.fleet[a.machine]
			message := scanRetryMessage(r, o.namespace)
			if !exists || (found && a.is(previous) && (previous.Result != resultRunning || previous.Message == message)) {
				continue
			}
			records[a.machine] = recordOf(scanEnd{attempt: a, result: resultRunning, message: message}, previous, "", now)
		}
		current.Machines = sortedRecords(records)
		return current
	})
	if err != nil {
		return err
	}
	if written {
		o.postCatalog(c, condition{}, notices, false)
	}
	var problems []error
	for _, end := range ends {
		r, found := c.Status.record(end.attempt.machine)
		if found && end.result == resultScanned && end.attempt.is(r) && r.Result == resultScanned {
			if err := o.applyScan(rules, entriesOf(r, c.Metadata.Name, end.devices, *rules)); err != nil {
				// The pod is kept, so the next pass reads its result again.
				problems = append(problems, err)
				continue
			}
		}
		if end.scan != nil {
			problems = append(problems, o.remove(*end.scan))
		}
	}
	problems = append(problems, o.replaceEndedScans(c, retries, w, now))
	return errors.Join(problems...)
}

// replaceEndedScans creates the next pod of each scan whose pod the
// kubelet ended and whose wait is over, and deletes the ended pod. A
// scan of a machine whose Machine is deleted gets no next pod.
func (o *operator) replaceEndedScans(c *catalog, retries []retry, w observed, now time.Time) error {
	var problems []error
	var notices []notice
	for _, r := range retries {
		a := scanOf(r.ended)
		if _, exists := w.fleet[a.machine]; !exists {
			problems = append(problems, o.remove(r.ended))
			continue
		}
		if !r.due(now) {
			continue
		}
		next := nextTry(r.ended)
		created, err := o.replaceEnded(r.ended, next)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if created && r.first() {
			notices = append(notices, notice{reason: reasonScanPodRecreated, message: fmt.Sprintf(
				"%s: the kubelet ended the scan pod %s/%s: %s, so the operator created the scan pod %s/%s for the same scan",
				a.machine, o.namespace, r.ended.Metadata.Name, r.ended.kubeletEnd(), o.namespace, next.Metadata.Name)})
		}
	}
	o.postCatalog(c, condition{}, notices, false)
	return errors.Join(problems...)
}

// fileMove moves a machine's record to a new catalog file without a scan.
type fileMove struct {
	machine, from, to string
}

// schedule reads the catalog file when a read is due, starts the scans
// that are needed, and writes both to the FirmwareCatalog's status. It
// returns when the next timed read of the catalog file is due.
func (o *operator) schedule(ctx context.Context, c *catalog, moved bool, busy map[string]bool, w observed, now time.Time) (time.Time, error) {
	due, next := o.refreshDue(c, moved, now)
	readiness, file, sum := c.Status.ready(), c.Status.File, c.Status.SHA256
	var read *fetched
	if due {
		f := o.refresh(ctx, c, now)
		read, next = &f, now.Add(o.discovery.interval)
		readiness, file, sum = f.ready, f.file, f.sha256
	}
	var starts []scanAttempt
	var fileMoves []fileMove
	var problems []error
	if readiness.Status == conditions.True && file != "" {
		for _, name := range sortedKeys(w.fleet) {
			m := w.fleet[name]
			if m.Status.Phase != machineReady || busy[name] {
				continue
			}
			a := o.currentScan(c, name, w)
			a.file = file
			r, found := c.Status.record(name)
			if found && a.is(r) {
				continue
			}
			if found && r.Boot == a.boot && r.Devices == a.devices && r.Image == a.image && r.Result == resultScanned && o.sameReleases(ctx, c, r, file, sum) {
				fileMoves = append(fileMoves, fileMove{machine: name, from: r.File, to: file})
				continue
			}
			if err := o.startScan(c, a, sum); err != nil {
				problems = append(problems, err)
				continue
			}
			starts = append(starts, a)
		}
	}

	var transitioned bool
	written, err := o.settleCatalog(c, func(current catalogStatus) catalogStatus {
		transitioned = false
		if read != nil {
			current.File, current.SHA256, current.ETag, current.Checked = read.file, read.sha256, read.etag, read.checked
			readiness.ObservedGeneration = c.Metadata.Generation
			readiness.LastTransitionTime = now.UTC().Truncate(time.Second)
			current.Conditions = slices.Clone(current.Conditions)
			transitioned = conditions.Set(&current.Conditions, readiness)
		}
		records := map[string]machineRecord{}
		for _, r := range current.Machines {
			records[r.Machine] = r
		}
		for _, a := range starts {
			previous, exists := records[a.machine]
			if exists && a.is(previous) {
				continue
			}
			records[a.machine] = machineRecord{
				Machine: a.machine, Boot: a.boot, Devices: a.devices, File: a.file, Image: a.image,
				GUIDs: previous.GUIDs, Releases: previous.Releases,
				Result: resultRunning, Message: "the scan pod " + o.namespace + "/" + scanPodName(a) + " is scanning the machine's devices",
				Time: stamp(now),
			}
		}
		for _, move := range fileMoves {
			r, exists := records[move.machine]
			if exists && r.Result == resultScanned && r.File == move.from {
				r.File = move.to
				records[move.machine] = r
			}
		}
		current.Machines = sortedRecords(records)
		return current
	})
	if err != nil {
		return next, errors.Join(append(problems, err)...)
	}
	if written {
		var notices []notice
		for _, a := range starts {
			notices = append(notices, notice{reason: reasonScanStarted, message: a.machine + ": the scan pod " +
				o.namespace + "/" + scanPodName(a) + " scans the machine's devices against the catalog file " + a.file})
		}
		o.postCatalog(c, readiness, notices, transitioned)
	}
	return next, errors.Join(problems...)
}

// sameReleases reports whether the catalog file offers the same releases
// for a machine's GUIDs as the file in its record. A file that cannot be
// read counts as different, so the machine is scanned.
func (o *operator) sameReleases(ctx context.Context, c *catalog, r machineRecord, file, sum string) bool {
	if r.Releases == "" {
		return false
	}
	index, err := o.indexOf(ctx, c, file, sum)
	if err != nil {
		fmt.Fprintf(os.Stderr, "firmware-operator: reading the catalog file of %s: %v\n", c.Metadata.Name, err)
		return false
	}
	return index.releasesHash(r.GUIDs) == r.Releases
}

// startScan creates the scan pod of an attempt. The pod name is derived
// from the attempt, so a 409 means that the attempt already has a pod.
func (o *operator) startScan(c *catalog, a scanAttempt, sum string) error {
	scan := buildScanPod(c, a, sum, o.namespace)
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(scan), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// settleCatalog writes the status that compose returns, unless it equals
// the current status. After a 409, compose runs again on the API
// server's copy. A FirmwareCatalog deleted during the pass is not an
// error.
func (o *operator) settleCatalog(c *catalog, compose func(catalogStatus) catalogStatus) (bool, error) {
	written, err := informer.SettleStatus[catalog](o.client, o.catalogs.Versions, catalogPath(c.Metadata.Name), c, func(current *catalog) bool {
		next := compose(current.Status)
		if reflect.DeepEqual(current.Status, next) {
			return false
		}
		current.APIVersion, current.Kind, current.Status = firmwareAPIVersion, catalogKind, next
		return true
	})
	if errors.Is(err, apiclient.ErrNotFound) {
		return false, nil
	}
	return written, err
}

func sortedRecords(records map[string]machineRecord) []machineRecord {
	var out []machineRecord
	for _, name := range sortedKeys(records) {
		out = append(out, records[name])
	}
	return out
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
