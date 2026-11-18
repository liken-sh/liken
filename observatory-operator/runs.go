package main

// The operator keeps the runs of each resource's procedures in memory,
// and the status writer copies them to the resource's
// status.procedures. A restarted operator loads them back from the
// stored statuses before any runner or trigger starts, and resumes each
// run that is Running.
//
// Runs are keyed by the object's UID as well as its name, so a Dome that
// a person deletes and creates again with the same name starts with no
// runs, and its activation runs again.

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
	// busy holds the trigger of the run in progress, by recordKey.
	busy map[string]string
	// changed is notified when a run changes or ends, which wakes the
	// status writer and every caller waiting in acquire.
	changed *changeNotifier
}

func newRunRecords(changed *changeNotifier) *runRecords {
	return &runRecords{runs: map[string]map[string]observatory.ProcedureRun{}, busy: map[string]string{}, changed: changed}
}

func recordKey(kind observatory.Kind, meta observatory.ObjectMeta) string {
	return kind.Name + "/" + meta.Name + "/" + meta.UID
}

func (r resource) record() string { return recordKey(r.kind, r.meta) }

// load loads the runs from the status.procedures of each resource.
func (r *runRecords) load(t *tree) {
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

// forget deletes the runs of each object that is no longer in the
// stores. It takes the snapshot while it holds the lock, so a put from a
// runner that saw a newer tree waits until forget returns and is not
// deleted. A put for a deleted object after forget returns is deleted by
// the next call.
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

func (r *runRecords) get(key, trigger string) (observatory.ProcedureRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[key][trigger]
	run.Actions = slices.Clone(run.Actions)
	return run, ok
}

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

// list returns the runs of one resource in trigger order: activation,
// deactivation, and then each item of spec.triggers.
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

// acquire marks the resource busy with this trigger's run, after
// waiting for any other run of the resource to release it, because two
// runs at once would send the device conflicting targets, such as a park
// and an unpark. Each time it starts to wait behind a different trigger,
// it calls waiting with that trigger.
func (r *runRecords) acquire(ctx context.Context, key, trigger string, waiting func(holder string)) error {
	reported := ""
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
		if holder != reported {
			reported = holder
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
