package main

// One pass answers every Firmware: it follows the rule's upload pods
// to their results, starts an upload for each new appearance of a
// device that the rule alone matches, reports each conflict, and
// writes the status. Then it answers every FirmwareCatalog (scans.go). The
// watches wake a pass (watch.go). A pass reads
// each Firmware from the watch's store, and from the API server when
// the store's copy is older than the operator's own last write, so a
// pass never starts an upload again because its own status write has
// not reached the watch yet.

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

	// discovery holds the flags of discovery, and catalogCaches what the
	// operator keeps in memory of each FirmwareCatalog (catalogfetch.go).
	discovery     discoverySettings
	catalogCaches map[string]*catalogCache

	// recorder posts the Events (events.go). Nil posts none.
	recorder *events.Recorder
}

func newOperator(client *apiclient.Client, namespace string, recorder *events.Recorder, discovery discoverySettings) *operator {
	return &operator{client: client, namespace: namespace, selectors: newSelectors(), recorder: recorder,
		discovery: discovery, catalogCaches: map[string]*catalogCache{}}
}

// pass answers every Firmware and every FirmwareCatalog, and answers the
// earliest time at which an upload or a scan in progress is due, or a
// jcat read, or the zero time when none is. A pass before the watches
// of the slices, the pods, the Machines, and the Disruptions hold their
// first read does nothing, because a pass that sees no pod for a device
// or a machine starts one, and a pass that sees no request for a
// machine's turn files one. The first read of each watch wakes another pass.
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
		// The stop ends the pass between two rules, so a shutdown waits
		// for the writes of one rule and no more.
		if ctx.Err() != nil {
			break
		}
		f := &rules[index]
		if f.Metadata.DeletionTimestamp != "" || f.Spec.Upload == nil {
			continue
		}
		due, err := o.answer(f, m, w, time.Now())
		if err != nil {
			problems = append(problems, fmt.Errorf("the Firmware %s: %w", f.Metadata.Name, err))
		}
		next = earliest(next, due)
	}
	if ctx.Err() != nil {
		return next, errors.Join(problems...)
	}
	due, err := o.discover(ctx, &rules, w, time.Now())
	return earliest(next, due), errors.Join(append(problems, err)...)
}

// answer brings one rule's status up to date, and answers when its
// earliest upload in progress is due.
func (o *operator) answer(f *firmware, m matching, w observed, now time.Time) (time.Time, error) {
	p := plan{valid: validity(m.invalid[f.Metadata.Name])}
	var problems []error
	var finished []pod
	var due time.Time

	// A port with an upload pod of this rule takes no new upload until
	// the pod is gone, so one device never has two uploads at once.
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
			// An operator that stopped between the pod's create and the
			// claim's left a pod with no claim, which never schedules.
			// So a pod that is not scheduled gets its claim again, and a
			// claim that exists answers 409.
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
				result: resultFailed, message: "the upload pod is gone; the operator does not run it again for this appearance"})
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
		if e, held := entryFor(f.Status, d.port()); held && sameAttempt(e, d.appearance, generation) && e.Result != resultConflict {
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
	// The pods are deleted after their results are in the status.
	for _, upload := range finished {
		problems = append(problems, o.remove(upload))
	}
	return due, errors.Join(problems...)
}

// settle writes the status that a plan gives a rule, and writes nothing
// when the status already holds it. A rule deleted during the pass
// needs no status.
func (o *operator) settle(f *firmware, p plan, now time.Time) error {
	var next firmwareStatus
	var notices []notice
	var transitioned bool
	written, err := informer.SettleStatus[firmware](o.client, o.firmwares.Versions, firmwarePath(f.Metadata.Name), f, func(held *firmware) bool {
		next, notices, transitioned = composeStatus(held, p, now)
		if !statusChanged(held.Status, next) {
			return false
		}
		held.APIVersion, held.Kind, held.Status = firmwareAPIVersion, firmwareKind, next
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

// validity answers the Valid condition of a rule whose selector and
// spec.machines compiled, or failed to with err.
func validity(err error) condition {
	if err != nil {
		return condition{Type: valid, Status: conditions.False, Reason: reasonSelectorInvalid, Message: err.Error()}
	}
	return condition{Type: valid, Status: conditions.True, Reason: reasonCompiled, Message: "the selector compiled"}
}

func entryFor(s firmwareStatus, at port) (deviceEntry, bool) {
	for _, e := range s.Devices {
		if e.Machine == at.machine && e.Address == at.address {
			return e, true
		}
	}
	return deviceEntry{}, false
}

// earliest answers the earlier of two times, where the zero time is no
// time at all.
func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
