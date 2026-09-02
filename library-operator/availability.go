package main

// One namespace's reporter is either on the bus or it is not,
// and that one signal stands for every Library of the namespace,
// because the catalog pod is the one process that reports them all.

import "sync"

// The desk that holds the last availability the bus carried for
// each namespace's reporter, keyed by namespace, with the loop's own
// wake beside it.
type reporters struct {
	mutex  sync.Mutex
	online map[string]bool
	wake   chan<- struct{}
}

func newReporters(wake chan<- struct{}) *reporters {
	return &reporters{online: map[string]bool{}, wake: wake}
}

// A change of the flag wakes the loop, so the pass that answers
// it reads the Libraries beside the reporter that just arrived or left;
// a repeat wakes nothing, because a reconnecting reporter republishes
// online on every session and the pass has already run.
func (r *reporters) mark(namespace string, online bool) {
	r.mutex.Lock()
	previous, had := r.online[namespace]
	r.online[namespace] = online
	r.mutex.Unlock()
	if !had || previous != online {
		poke(r.wake)
	}
}

// A namespace the desk holds nothing for reads offline, which is
// the state before its catalog pod first connects.
func (r *reporters) onlineFor(namespace string) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.online[namespace]
}
