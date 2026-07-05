package main

// Tests for the reboot watcher's decision: when it fires and what it
// carries. The shutdown sequence it triggers (kill, unmount, the
// reboot syscall) is PID-1 territory and belongs to the QEMU harness.

import (
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

func TestWatchForRebootIntentDeliversExactlyOne(t *testing.T) {
	dir := t.TempDir()
	requests := make(chan machine.RebootIntent, 1)
	go watchForRebootIntent(dir, time.Millisecond, requests)

	// A few empty polls happen first: no file, no delivery.
	select {
	case got := <-requests:
		t.Fatalf("nothing was requested yet: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	want := machine.RebootIntent{Reason: "applying the staged spec", ManifestHash: "abc"}
	if err := machine.WriteRebootIntent(dir, &want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-requests:
		if got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher never delivered the intent")
	}
}
