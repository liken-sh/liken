package main

// The fake servers' shim. indi-shim starts or stops a driver when the
// kubelet writes the server pod's annotation into its file again, about
// a second after the change. A device pod that goes before its driver
// stops ends the driver's connection, and indiserver reads EOF and
// starts the driver again. The fake applies each change at once unless
// a test sets a delay, and records each driver that lost its pod while
// the shim still listed it.

import (
	"slices"
	"time"
)

// shim applies the devices that a server pod's annotation names: at
// once, or after shimDelay. The caller holds w.mu.
func (w *indiWorld) shim(s *fakeServer, want []string) {
	if slices.Equal(want, s.target) {
		return
	}
	s.target = want
	if w.shimDelay == 0 {
		s.applied = want
		return
	}
	time.AfterFunc(w.shimDelay, func() {
		w.followMu.Lock()
		defer w.followMu.Unlock()
		w.mu.Lock()
		if slices.Equal(s.target, want) {
			s.applied = want
		}
		w.mu.Unlock()
		w.sync(w.last)
	})
}

// slowShim makes each shim apply a change of its annotation after a
// delay.
func (w *indiWorld) slowShim(delay time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shimDelay = delay
}

// restarted answers the drivers that lost their device pod before the
// shim stopped them, as "<server> <pod>".
func (w *indiWorld) restarted() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.restarts)
}
