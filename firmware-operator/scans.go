package main

// One pass answers each FirmwareCatalog in two steps. First it follows the
// FirmwareCatalog's scan pods to their results, and writes each result to the
// machine's record and to the discovered Firmware objects. Then it
// reads the jcat when a read is due, and starts a scan of each Ready
// machine that needs one, and writes both to the status in one write.
//
// A machine needs a scan against a FirmwareCatalog when one of these changed
// since its record: the time it booted, the set of device names in its
// slice, or the catalog file. A version changes only across a reboot or
// a flash, and a device number that changes on a replug does not count.
// When only the file changed, and the new file offers the same releases
// for the GUIDs the machine reported last, the operator records the new
// file and scans nothing, and moves the machine's entries in the
// Firmware objects to the new file too (discovered.go). Each attempt, a boot, a device set, and a
// file, runs once, so a failed scan waits for one of the three to
// change.

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

// discover answers every FirmwareCatalog, and answers the earliest time at
// which a scan's deadline or a jcat read is due.
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
		// The stop ends the pass between two FirmwareCatalogs, so a shutdown
		// waits for the writes of one FirmwareCatalog and no more.
		if ctx.Err() != nil || c.Metadata.DeletionTimestamp != "" {
			continue
		}
		due, err := o.answerCatalog(ctx, c, rules, w, now)
		if err != nil {
			problems = append(problems, fmt.Errorf("the FirmwareCatalog %s: %w", c.Metadata.Name, err))
		}
		next = earliest(next, due)
	}
	o.forgetCaches(present)
	if ctx.Err() == nil {
		problems = append(problems, o.forgetMachines(*rules, w.fleet))
	}
	if ctx.Err() == nil {
		due, err := o.approveAll(*rules, w, byName, now)
		next = earliest(next, due)
		problems = append(problems, err)
	}
	return next, errors.Join(problems...)
}

// answerCatalog brings one FirmwareCatalog's status and its scans up to date.
func (o *operator) answerCatalog(ctx context.Context, c *catalog, rules *[]firmware, w observed, now time.Time) (time.Time, error) {
	ends, busy, due, err := o.endsOf(c, w.scans[c.Metadata.Name], now)
	problems := []error{err}
	if err := o.recordEnds(ctx, c, ends, rules, w, now); err != nil {
		return due, errors.Join(append(problems, err)...)
	}
	moved := slices.ContainsFunc(ends, func(end scanEnd) bool { return end.result == resultCatalogMoved })
	next, err := o.schedule(ctx, c, moved, busy, w, now)
	problems = append(problems, err, o.refileEntries(*rules, c))
	return earliest(due, next), errors.Join(problems...)
}

// recordEnds writes the end of each attempt to the FirmwareCatalog's status and
// removes the record of each machine whose Machine is deleted. Then it
// writes each answered scan to the discovered Firmware objects, and
// deletes each finished pod whose result is written.
func (o *operator) recordEnds(ctx context.Context, c *catalog, ends []scanEnd, rules *[]firmware, w observed, now time.Time) error {
	releases := map[string]string{}
	for _, end := range ends {
		if end.result == resultScanned && end.attempt.file == c.Status.File {
			index, err := o.indexOf(ctx, c, c.Status.File, c.Status.SHA256)
			if err != nil {
				// With no hash, the next catalog file scans the machine
				// again, which costs a scan and loses nothing.
				fmt.Fprintf(os.Stderr, "firmware-operator: reading the catalog file of %s: %v\n", c.Metadata.Name, err)
				continue
			}
			releases[end.attempt.machine] = index.releasesHash(guidsOf(end.devices))
		}
	}
	var notices []notice
	written, err := o.settleCatalog(c, func(held catalogStatus) catalogStatus {
		notices = nil
		records := map[string]machineRecord{}
		for _, r := range held.Machines {
			if _, exists := w.fleet[r.Machine]; exists {
				records[r.Machine] = r
			}
		}
		for _, end := range ends {
			previous, held := records[end.attempt.machine]
			_, exists := w.fleet[end.attempt.machine]
			if !exists || (held && end.attempt.is(previous) && previous.Result != resultRunning) {
				continue
			}
			records[end.attempt.machine] = recordOf(end, previous, releases[end.attempt.machine], now)
			notices = append(notices, endNotice(end))
		}
		held.Machines = sortedRecords(records)
		return held
	})
	if err != nil {
		return err
	}
	if written {
		o.postCatalog(c, condition{}, notices, false)
	}
	var problems []error
	for _, end := range ends {
		r, held := c.Status.record(end.attempt.machine)
		if held && end.result == resultScanned && end.attempt.is(r) && r.Result == resultScanned {
			if err := o.applyScan(rules, entriesOf(r, c.Metadata.Name, end.devices, *rules)); err != nil {
				// The pod stays, so the next pass reads its result again.
				problems = append(problems, err)
				continue
			}
		}
		if end.scan != nil {
			problems = append(problems, o.remove(*end.scan))
		}
	}
	return errors.Join(problems...)
}

// refile moves a machine's record to a new catalog file with no scan,
// because the file offers the same releases for its GUIDs.
type refile struct {
	machine, from, to string
}

// schedule reads the jcat when a read is due, starts a scan of each
// Ready machine that needs one, and writes both. It answers when the
// next timed read of the jcat is due.
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
	var refiles []refile
	var problems []error
	if readiness.Status == conditions.True && file != "" {
		for _, name := range sortedKeys(w.fleet) {
			m := w.fleet[name]
			if m.Status.Phase != machineReady || busy[name] {
				continue
			}
			a := scanAttempt{catalog: c.Metadata.Name, machine: name, boot: m.Status.Boot.Time, devices: w.deviceSets[name], file: file, image: o.discovery.scanImage}
			r, held := c.Status.record(name)
			if held && a.is(r) {
				continue
			}
			if held && r.Boot == a.boot && r.Devices == a.devices && r.Image == a.image && r.Result == resultScanned && o.sameReleases(ctx, c, r, file, sum) {
				refiles = append(refiles, refile{machine: name, from: r.File, to: file})
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
	written, err := o.settleCatalog(c, func(held catalogStatus) catalogStatus {
		transitioned = false
		if read != nil {
			held.File, held.SHA256, held.ETag, held.Checked = read.file, read.sha256, read.etag, read.checked
			readiness.ObservedGeneration = c.Metadata.Generation
			readiness.LastTransitionTime = now.UTC().Truncate(time.Second)
			held.Conditions = slices.Clone(held.Conditions)
			transitioned = conditions.Set(&held.Conditions, readiness)
		}
		records := map[string]machineRecord{}
		for _, r := range held.Machines {
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
				Result: resultRunning, Message: "the scan pod " + o.namespace + "/" + scanPodName(a) + " reads the machine's devices",
				Time: stamp(now),
			}
		}
		for _, move := range refiles {
			r, exists := records[move.machine]
			if exists && r.Result == resultScanned && r.File == move.from {
				r.File = move.to
				records[move.machine] = r
			}
		}
		held.Machines = sortedRecords(records)
		return held
	})
	if err != nil {
		return next, errors.Join(append(problems, err)...)
	}
	if written {
		var notices []notice
		for _, a := range starts {
			notices = append(notices, notice{reason: reasonScanStarted, message: a.machine + ": the scan pod " +
				o.namespace + "/" + scanPodName(a) + " reads the machine's devices against " + a.file})
		}
		o.postCatalog(c, readiness, notices, transitioned)
	}
	return next, errors.Join(problems...)
}

// sameReleases reports whether a catalog file offers the same releases
// for a machine's GUIDs as the file of its record. A file the operator
// cannot read counts as different, so the machine is scanned.
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

// startScan creates the pod of one attempt. A pod that already exists
// under the name answers 409, which means the attempt runs.
func (o *operator) startScan(c *catalog, a scanAttempt, sum string) error {
	scan := buildScanPod(c, a, sum, o.namespace)
	err := o.client.RequestJSON(http.MethodPost, podsPathIn(o.namespace), mustJSON(scan), nil)
	if errors.Is(err, apiclient.ErrConflict) {
		return nil
	}
	return err
}

// settleCatalog writes the status that compose gives a FirmwareCatalog, and
// writes nothing when the status already holds it. compose runs again
// on the API server's copy after a 409. A FirmwareCatalog deleted during the
// pass needs no status.
func (o *operator) settleCatalog(c *catalog, compose func(catalogStatus) catalogStatus) (bool, error) {
	written, err := informer.SettleStatus[catalog](o.client, o.catalogs.Versions, catalogPath(c.Metadata.Name), c, func(held *catalog) bool {
		next := compose(held.Status)
		if reflect.DeepEqual(held.Status, next) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = firmwareAPIVersion, catalogKind, next
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
