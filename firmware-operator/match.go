package main

// A pass reads the current state from the informer caches, then
// matches each upload rule against each device. CEL validation in the
// CRD checks one object at a time, so the API server accepts two rules
// that match the same device. match records every rule that matches
// each port, and the pass (pass.go) runs neither rule on a port with
// two and reports the conflict in the status of both.

import (
	"context"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// observed is the state that the informer caches hold at the start of a
// pass.
type observed struct {
	devices []device
	present map[appearance]bool
	// machines holds each Machine's labels, keyed by name.
	machines map[string]map[string]string
	// uploads holds the upload pods, keyed by the rule each runs.
	uploads map[string][]pod

	// fleet holds every Machine, keyed by name.
	fleet map[string]machine
	// deviceSets holds the hash of the device names in each
	// machine's slices, keyed by machine name (deviceSetHash).
	deviceSets map[string]string
	// scans holds the scan pods, keyed by the FirmwareCatalog each
	// one reads.
	scans map[string][]pod
	// rehearsals holds the rehearsal pods, keyed by the Firmware each
	// one rehearses.
	rehearsals map[string][]pod
	// applies holds the apply pods, and disruptions holds the
	// Disruptions that request a reboot turn. Both are keyed by the
	// Firmware that each one applies.
	applies     map[string][]pod
	disruptions map[string][]disruption
}

func (o *operator) observe() observed {
	w := observed{present: map[appearance]bool{}, machines: map[string]map[string]string{}, uploads: map[string][]pod{},
		fleet: map[string]machine{}, deviceSets: map[string]string{}, scans: map[string][]pod{}, rehearsals: map[string][]pod{},
		applies: map[string][]pod{}, disruptions: map[string][]disruption{}}
	names := map[string][]string{}
	for _, slice := range informer.CachedList[resourceSlice](o.slices) {
		for _, d := range devicesOf(slice) {
			w.devices = append(w.devices, d)
			w.present[d.appearance] = true
		}
		for _, d := range slice.Spec.Devices {
			names[slice.Spec.NodeName] = append(names[slice.Spec.NodeName], d.Name)
		}
	}
	for _, m := range informer.CachedList[machine](o.machines) {
		w.machines[m.Metadata.Name] = m.Metadata.Labels
		w.fleet[m.Metadata.Name] = m
		w.deviceSets[m.Metadata.Name] = deviceSetHash(names[m.Metadata.Name])
	}
	for _, upload := range informer.CachedList[pod](o.pods) {
		rule, _, _ := uploadOf(upload)
		w.uploads[rule] = append(w.uploads[rule], upload)
	}
	for _, scan := range informer.CachedList[pod](o.scanPods) {
		a := scanOf(scan)
		w.scans[a.catalog] = append(w.scans[a.catalog], scan)
	}
	for _, p := range informer.CachedList[pod](o.rehearsalPods) {
		a := rehearsalOf(p)
		w.rehearsals[a.firmware] = append(w.rehearsals[a.firmware], p)
	}
	for _, p := range informer.CachedList[pod](o.applyPods) {
		a := applyOf(p)
		w.applies[a.firmware] = append(w.applies[a.firmware], p)
	}
	for _, d := range informer.CachedList[disruption](o.disruptions) {
		rule := d.Metadata.Labels[disruptionLabel]
		w.disruptions[rule] = append(w.disruptions[rule], d)
	}
	return w
}

// matching is the result of matching every rule against every device.
type matching struct {
	// invalid holds the error of each rule whose selector or
	// spec.machines does not compile. Such a rule runs nowhere.
	invalid map[string]error
	// devices holds the devices that each valid rule matches, keyed
	// by rule name.
	devices map[string][]device
	// rules holds the names of the rules that match a device, keyed
	// by the port the device is in.
	rules map[port][]string
}

func (o *operator) match(ctx context.Context, rules []firmware, w observed) matching {
	m := matching{invalid: map[string]error{}, devices: map[string][]device{}, rules: map[port][]string{}}
	inUse := map[string]bool{}
	for index := range rules {
		f := &rules[index]
		// Only an upload rule has a selector.
		if f.Metadata.DeletionTimestamp != "" || f.Spec.Upload == nil {
			continue
		}
		inUse[f.Spec.Selector] = true
		selector, err := o.selectors.compile(f.Spec.Selector)
		if err != nil {
			m.invalid[f.Metadata.Name] = err
			continue
		}
		applies, err := filterMachines(f.Spec.Machines, w.machines)
		if err != nil {
			m.invalid[f.Metadata.Name] = err
			continue
		}
		for _, d := range w.devices {
			if applies(d.machine) && matches(ctx, f.Metadata.Name, selector, d) {
				m.devices[f.Metadata.Name] = append(m.devices[f.Metadata.Name], d)
				m.rules[d.port()] = append(m.rules[d.port()], f.Metadata.Name)
			}
		}
	}
	o.selectors.forget(inUse)
	return m
}
