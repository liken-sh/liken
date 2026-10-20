package main

// A pass first reads the world from the watches' stores: every device
// that an upload can reach, the labels of every Machine, and every
// upload pod. Then it matches each rule against each device. CEL
// validates one object at a time, so the API server cannot refuse a
// second rule that matches the same device as the first. The operator
// finds that here, runs neither rule on the device, and reports the
// conflict in the status of both.

import (
	"context"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// observed is what the watches hold at the start of a pass.
type observed struct {
	devices  []device
	present  map[appearance]bool
	machines map[string]map[string]string
	// uploads holds the upload pods by the rule that each one runs.
	uploads map[string][]pod
}

func (o *operator) observe() observed {
	w := observed{present: map[appearance]bool{}, machines: map[string]map[string]string{}, uploads: map[string][]pod{}}
	for _, slice := range informer.CachedList[resourceSlice](o.slices) {
		for _, d := range devicesOf(slice) {
			w.devices = append(w.devices, d)
			w.present[d.appearance] = true
		}
	}
	for _, m := range informer.CachedList[machine](o.machines) {
		w.machines[m.Metadata.Name] = m.Metadata.Labels
	}
	for _, upload := range informer.CachedList[pod](o.pods) {
		rule, _, _ := uploadOf(upload)
		w.uploads[rule] = append(w.uploads[rule], upload)
	}
	return w
}

// matching is every rule matched against every device.
type matching struct {
	// invalid holds the error of each rule whose selector or
	// spec.machines does not compile. Such a rule runs nowhere.
	invalid map[string]error
	// devices holds the devices each valid rule matches.
	devices map[string][]device
	// rules holds the names of the rules that match the device in each
	// port.
	rules map[port][]string
}

func (o *operator) match(ctx context.Context, rules []firmware, w observed) matching {
	m := matching{invalid: map[string]error{}, devices: map[string][]device{}, rules: map[port][]string{}}
	inUse := map[string]bool{}
	for index := range rules {
		f := &rules[index]
		if f.Metadata.DeletionTimestamp != "" {
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
