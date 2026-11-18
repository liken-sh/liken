package main

// The fake of indi-shim, the program in the server pod that starts and
// stops drivers. The real shim reads the list of devices from the pod's
// annotation through a file that the kubelet updates some time after
// the annotation changes. When a device pod is deleted while the shim
// still runs its driver, the driver's connection closes, indiserver
// reads EOF, and it starts the driver again. The fake applies each
// change at once unless a test sets a delay with slowShim, and records
// each such restart.

import (
	"slices"
	"time"
)

// shim starts and stops drivers to match the devices that a server pod's
// annotation lists, immediately or after shimDelay. The caller holds
// w.mu.
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

// slowShim makes the shim apply each change of the annotation after
// the given delay.
func (w *indiWorld) slowShim(delay time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.shimDelay = delay
}

// restarted returns the drivers whose device pod was deleted before the
// shim stopped them, as "<server> <pod>".
func (w *indiWorld) restarted() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.restarts)
}
