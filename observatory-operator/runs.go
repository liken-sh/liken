package main

// The record of each procedure's runs. The operator keeps the runs in
// memory, and the status writer writes them to each resource's
// status.procedures. The status is the record that outlives the
// operator: a new copy loads the runs from the stored statuses before
// any runner or trigger runs, and resumes a run that was Running.
//
// The records belong to one object, by its UID. A Dome that a person
// deletes and creates again with the same name is a new object, and
// starts with no runs: the runs of the old one answered transitions
// that the new one never saw, and its activation must run.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

type runRecords struct {
	mu sync.Mutex
	// runs holds the last run of each trigger, by recordKey and then
	// by trigger.
	runs map[string]map[string]observatory.ProcedureRun
	// busy holds the trigger of the run that goes on now, by
	// recordKey, so a resource runs one procedure at a time.
	busy map[string]string
	// changed rings on each change of a record, so the status writer
	// writes it, and a wait for another resource's run reads it.
	changed *bell
}

func newRunRecords(changed *bell) *runRecords {
	return &runRecords{runs: map[string]map[string]observatory.ProcedureRun{}, busy: map[string]string{}, changed: changed}
}

// recordKey names the records of one object: its kind, its name, and
// its UID.
func recordKey(kind observatory.Kind, meta observatory.ObjectMeta) string {
	return kind.Name + "/" + meta.Name + "/" + meta.UID
}

// record names the records of a resource's object.
func (r resource) record() string { return recordKey(r.kind, r.meta) }

// seed loads the runs from the status of each resource with
// procedures. A status is part of its object, so each record loads
// under the UID of the object that holds it.
func (r *runRecords) seed(t *tree) {
	r.mu.Lock()
	defer r.mu.Unlock()
	load := func(key string, runs []observatory.ProcedureRun) {
		for _, run := range runs {
			if r.runs[key] == nil {
				r.runs[key] = map[string]observatory.ProcedureRun{}
			}
			r.runs[key][run.Trigger] = run
		}
	}
	for _, d := range t.devices {
		load(recordKey(d.kind, d.object.Metadata), d.object.Status.Procedures)
	}
	for _, telescope := range t.telescopes {
		load(recordKey(observatory.TelescopeKind, telescope.Metadata), telescope.Status.Procedures)
	}
	for _, site := range t.observatories {
		load(recordKey(observatory.ObservatoryKind, site.Metadata), site.Status.Procedures)
	}
}

// forget drops the records of each object that the stores no longer
// hold. It reads the tree while it holds the records, so a run that
// read a newer tree, with an object this pass has not seen, writes its
// record only after the pass. A run of a deleted object that writes
// after the pass leaves a record that the next pass drops.
func (r *runRecords) forget(snapshot func() *tree) {
	r.mu.Lock()
	defer r.mu.Unlock()
	live := map[string]bool{}
	for _, res := range snapshot().withProcedures() {
		live[res.record()] = true
	}
	for key := range r.runs {
		if !live[key] {
			delete(r.runs, key)
		}
	}
}

// get answers the last run of one trigger of one resource.
func (r *runRecords) get(key, trigger string) (observatory.ProcedureRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[key][trigger]
	run.Actions = slices.Clone(run.Actions)
	return run, ok
}

// put records a run, and rings the bell.
func (r *runRecords) put(key string, run observatory.ProcedureRun) {
	r.mu.Lock()
	if r.runs[key] == nil {
		r.runs[key] = map[string]observatory.ProcedureRun{}
	}
	run.Actions = slices.Clone(run.Actions)
	r.runs[key][run.Trigger] = run
	r.mu.Unlock()
	r.changed.notify()
}

// drop removes the record of one trigger's run, and rings the bell.
func (r *runRecords) drop(key, trigger string) {
	r.mu.Lock()
	delete(r.runs[key], trigger)
	r.mu.Unlock()
	r.changed.notify()
}

// list answers the runs of one resource, in the order of its triggers:
// activation, deactivation, and then each item of spec.triggers.
func (r *runRecords) list(key string) []observatory.ProcedureRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []observatory.ProcedureRun
	for _, run := range r.runs[key] {
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b observatory.ProcedureRun) int { return triggerOrder(a.Trigger) - triggerOrder(b.Trigger) })
	return out
}

func triggerOrder(trigger string) int {
	switch trigger {
	case observatory.TriggerActivation:
		return 0
	case observatory.TriggerDeactivation:
		return 1
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(trigger, "triggers["), "]"))
	return 2 + n
}

// acquire takes a resource's turn for one trigger's run, and waits
// while another run of the resource goes on, or until ctx ends. Two
// runs of one resource would send its device two targets at once, such
// as a park from the weather and an unpark from the end of its for.
// waiting is called with the trigger whose run goes on, each time the
// run waits behind another trigger.
func (r *runRecords) acquire(ctx context.Context, key, trigger string, waiting func(holder string)) error {
	told := ""
	for {
		wake := r.changed.wait()
		r.mu.Lock()
		holder, held := r.busy[key]
		if !held {
			r.busy[key] = trigger
			r.mu.Unlock()
			return nil
		}
		r.mu.Unlock()
		if holder != told {
			told = holder
			waiting(holder)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-wake:
		}
	}
}

func (r *runRecords) release(key string) {
	r.mu.Lock()
	delete(r.busy, key)
	r.mu.Unlock()
	r.changed.notify()
}
