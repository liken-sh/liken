package main

// A pass reconciles every Firmware and then every FirmwareCatalog
// (scans.go). A watch event, a deadline, or a retry starts a pass
// (watch.go).
//
// A pass reads each Firmware from the watch's store, but reads it from
// the API server when the store's copy is older than the operator's last
// write. Otherwise a pass could start a second upload because the watch
// had not yet sent the status write of the previous pass.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

type operator struct {
	client *apiclient.Client

	// namespace is the operator's own, where the upload pods run.
	namespace string

	firmwares     informer.Held
	catalogs      informer.Held
	slices        informer.View
	pods          informer.View
	scanPods      informer.View
	rehearsalPods informer.View
	applyPods     informer.View
	machines      informer.View
	disruptions   informer.View

	selectors *selectors

	// discovery has the settings from flags. catalogCaches caches the
	// catalog file of each FirmwareCatalog (catalogfetch.go).
	discovery     discoverySettings
	catalogCaches map[string]*catalogCache

	// recorder posts the Events (events.go). A nil recorder posts none.
	recorder *events.Recorder
}

func newOperator(client *apiclient.Client, namespace string, recorder *events.Recorder, discovery discoverySettings) *operator {
	return &operator{client: client, namespace: namespace, selectors: newSelectors(), recorder: recorder,
		discovery: discovery, catalogCaches: map[string]*catalogCache{}}
}

// pass returns the earliest time at which an upload or a scan in
// progress is due, or a catalog file read is due, or the zero time when
// none is.
//
// A pass does nothing until the watches of the ResourceSlices, the
// pods, the Machines, and the Disruptions have each finished their
// first read. Before that, a pass could start a second upload or scan
// because it cannot see the pod of the first, or create a second
// Disruption. Each watch starts a pass when its first read ends.
func (o *operator) pass(ctx context.Context) (time.Time, error) {
	if !o.slices.Ready() || !o.pods.Ready() || !o.scanPods.Ready() || !o.rehearsalPods.Ready() || !o.applyPods.Ready() ||
		!o.machines.Ready() || !o.disruptions.Ready() {
		return time.Time{}, nil
	}
	rules, err := informer.List[firmware](o.client, o.firmwares, firmwaresPath, firmwarePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("listing the Firmware objects: %w", err)
	}
	w := o.observe()
	m := o.match(ctx, rules, w)
	var next time.Time
	var problems []error
	for index := range rules {
		// A shutdown waits for the writes of at most one Firmware.
		if ctx.Err() != nil {
			break
		}
		f := &rules[index]
		if f.Metadata.DeletionTimestamp != "" || f.Spec.Upload == nil {
			continue
		}
		due, err := o.reconcileFirmware(f, m, w, time.Now())
		if err != nil {
			problems = append(problems, fmt.Errorf("reconciling the Firmware %s: %w", f.Metadata.Name, err))
		}
		next = earliest(next, due)
	}
	if ctx.Err() != nil {
		return next, errors.Join(problems...)
	}
	due, err := o.discover(ctx, &rules, w, time.Now())
	return earliest(next, due), errors.Join(append(problems, err)...)
}

// reconcileFirmware reads the results of a Firmware's upload pods,
// starts an upload for each new appearance of a device that only this
// Firmware matches, records a conflict for each device that another
// Firmware also matches, and writes the status. It returns when the
// earliest upload in progress is due.
func (o *operator) reconcileFirmware(f *firmware, m matching, w observed, now time.Time) (time.Time, error) {
	p := plan{valid: validity(m.invalid[f.Metadata.Name])}
	var problems []error
	var finished []pod
	var due time.Time

	// A port that has an upload pod of this Firmware gets no new upload
	// until the pod is gone, so one device never has two uploads at once.
	busy := map[port]bool{}
	for _, upload := range w.uploads[f.Metadata.Name] {
		_, generation, a := uploadOf(upload)
		busy[a.port()] = true
		if upload.Metadata.DeletionTimestamp != "" {
			continue
		}
		v := follow(upload, w.present[a], now)
		if !v.decided() {
			due = earliest(due, v.due)
			// When the operator stopped between creating the pod and its
			// claim, the pod never schedules. So the claim of every
			// unscheduled Pending pod is created again, and a 409 means
			// that it exists.
			if upload.Status.Phase == podPending && !upload.scheduled() {
				problems = append(problems, o.claim(upload))
			}
			continue
		}
		p.results = append(p.results, attempt{appearance: a, device: upload.Metadata.Annotations[deviceKey],
			generation: generation, result: v.result, message: v.message})
		finished = append(finished, upload)
	}

	for _, e := range f.Status.Devices {
		a := appearance{machine: e.Machine, address: e.Address, deviceNumber: e.DeviceNumber}
		if e.Result != resultRunning || busy[a.port()] {
			continue
		}
		gone, err := o.podGone(uploadPodName(f.Metadata.Name, e.Generation, a))
		if err != nil {
			return time.Time{}, err
		}
		if gone {
			p.results = append(p.results, attempt{appearance: a, device: e.Device, generation: e.Generation,
				result: resultFailed, message: "the upload pod no longer exists, and the operator does not run it again until the device enumerates again"})
		}
	}

	generation := f.Metadata.Generation
	for _, d := range m.devices[f.Metadata.Name] {
		if busy[d.port()] {
			continue
		}
		a := attempt{appearance: d.appearance, device: d.name, generation: generation}
		if others := slices.DeleteFunc(slices.Clone(m.rules[d.port()]), func(name string) bool { return name == f.Metadata.Name }); len(others) > 0 {
			a.result = resultConflict
			a.message = "the Firmware objects " + strings.Join(others, ", ") + " also match this device, so no upload runs"
			p.conflicts = append(p.conflicts, a)
			continue
		}
		if e, found := entryFor(f.Status, d.port()); found && sameAttempt(e, d.appearance, generation) && e.Result != resultConflict {
			continue
		}
		name, err := o.start(f, d)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		a.result, a.message = resultRunning, "the upload pod "+o.namespace+"/"+name+" runs the loader"
		p.starts = append(p.starts, a)
	}

	if err := o.settle(f, p, now); err != nil {
		return time.Time{}, errors.Join(append(problems, err)...)
	}
	// A finished pod is deleted only after its result is in the status.
	for _, upload := range finished {
		problems = append(problems, o.remove(upload))
	}
	return due, errors.Join(problems...)
}

// settle writes the status that the plan gives a Firmware, unless it
// equals the current status. A Firmware deleted during the pass is not
// an error.
func (o *operator) settle(f *firmware, p plan, now time.Time) error {
	var next firmwareStatus
	var notices []notice
	var transitioned bool
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(current *firmware) bool {
		next, notices, transitioned = composeStatus(current, p, now)
		if !statusChanged(current.Status, next) {
			return false
		}
		current.APIVersion, current.Kind, current.Status = firmwareAPIVersion, firmwareKind, next
		return true
	})
	if written {
		o.post(f, next.valid(), notices, transitioned)
	}
	if errors.Is(err, apiclient.ErrNotFound) {
		return nil
	}
	return err
}

// validity returns the Valid condition of a Firmware. err is the
// failure to compile its selector or spec.machines.
func validity(err error) condition {
	if err != nil {
		return condition{Type: valid, Status: conditions.False, Reason: reasonSelectorInvalid, Message: err.Error()}
	}
	return condition{Type: valid, Status: conditions.True, Reason: reasonCompiled, Message: "The selectors compile"}
}

func entryFor(s firmwareStatus, at port) (deviceEntry, bool) {
	for _, e := range s.Devices {
		if e.Machine == at.machine && e.Address == at.address {
			return e, true
		}
	}
	return deviceEntry{}, false
}

// earliest returns the earlier of two times. The zero time means no
// time, so it loses to any other.
func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
