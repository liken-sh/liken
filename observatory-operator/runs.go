package main

// The record of each procedure's runs. The operator keeps the runs in
// memory, and the status writer writes them to each resource's
// status.procedures. The status is the record that outlives the
// operator: a new copy loads the runs from the stored statuses before
// any runner or trigger runs, and resumes a run that was Running.

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
	// runs holds the last run of each trigger, by resource key and
	// then by trigger.
	runs map[string]map[string]observatory.ProcedureRun
	// busy holds each trigger whose run goes on now, as key/trigger,
	// so one trigger has one run at a time.
	busy map[string]bool
	// changed rings on each change of a record, so the status writer
	// writes it, and a wait for another resource's run reads it.
	changed *bell
}

func newRunRecords(changed *bell) *runRecords {
	return &runRecords{runs: map[string]map[string]observatory.ProcedureRun{}, busy: map[string]bool{}, changed: changed}
}

// seed loads the runs from the status of each resource with
// procedures.
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
		load(d.key(), d.object.Status.Procedures)
	}
	for name, telescope := range t.telescopes {
		load(observatory.TelescopeKind.Name+"/"+name, telescope.Status.Procedures)
	}
	for name, site := range t.observatories {
		load(observatory.ObservatoryKind.Name+"/"+name, site.Status.Procedures)
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

// list answers the runs of one resource, in the order of its triggers:
// activation, deactivation, and then each item of spec.on.
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
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(trigger, "on["), "]"))
	return 2 + n
}

// acquire takes one trigger of one resource for a run, and waits while
// another run of it goes on, or until ctx ends.
func (r *runRecords) acquire(ctx context.Context, key, trigger string) error {
	id := key + "/" + trigger
	for {
		wake := r.changed.wait()
		r.mu.Lock()
		if !r.busy[id] {
			r.busy[id] = true
			r.mu.Unlock()
			return nil
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-wake:
		}
	}
}

func (r *runRecords) release(key, trigger string) {
	r.mu.Lock()
	delete(r.busy, key+"/"+trigger)
	r.mu.Unlock()
	r.changed.notify()
}
