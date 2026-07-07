package main

// Tests for the reboot watcher's decision: when it fires and what it
// carries. The shutdown sequence it triggers (kill, unmount, the
// reboot syscall) is PID-1 territory and belongs to the QEMU harness.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

func TestWatchForRebootIntentDeliversExactlyOne(t *testing.T) {
	dir := t.TempDir()
	requests := make(chan machine.RebootIntent, 1)
	go watchForRebootIntent(t.Context(), dir, time.Millisecond, requests)

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

func TestWatchForRebootIntentHonorsAnUnreadableIntent(t *testing.T) {
	// The file's presence is the trigger; content only improves the
	// message. A garbled intent must still reboot the machine rather
	// than strand a request.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reboot-intent.yaml"), []byte("{garbled"), 0o644); err != nil {
		t.Fatal(err)
	}
	requests := make(chan machine.RebootIntent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchForRebootIntent(ctx, dir, time.Millisecond, requests); err != nil {
		t.Fatal(err)
	}
	intent := <-requests
	if intent.Reason == "" {
		t.Error("an unreadable intent still carries a reason for the console")
	}
}
