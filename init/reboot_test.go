package main

// Tests for the intent watcher's decisions: when each disruption
// kind fires and what it carries. The shutdown sequence a reboot
// triggers (kill, unmount, the reboot syscall) is PID-1 territory
// and belongs to the QEMU harness.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chrisguidry/liken/machine"
)

// watchIntents arms the watcher over a tempdir with fast polls and
// hands back both channels.
func watchIntents(t *testing.T) (string, chan machine.RebootIntent, chan machine.RestartIntent) {
	t.Helper()
	dir := t.TempDir()
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	go watchForOperatorIntents(t.Context(), dir, time.Millisecond, reboots, restarts)
	return dir, reboots, restarts
}

func TestWatchDeliversARebootIntent(t *testing.T) {
	dir, reboots, _ := watchIntents(t)

	// A few empty polls happen first: no file, no delivery.
	select {
	case got := <-reboots:
		t.Fatalf("nothing was requested yet: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}

	want := machine.RebootIntent{Reason: "applying the staged spec", ManifestHash: "abc"}
	if err := machine.WriteRebootIntent(dir, &want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-reboots:
		if got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher never delivered the intent")
	}
}

func TestWatchHonorsAnUnreadableRebootIntent(t *testing.T) {
	// The file's presence is the trigger; content only improves the
	// message. A garbled intent must still reboot the machine rather
	// than strand a request.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reboot-intent.yaml"), []byte("{garbled"), 0o644); err != nil {
		t.Fatal(err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchForOperatorIntents(ctx, dir, time.Millisecond, reboots, restarts); err != nil {
		t.Fatal(err)
	}
	intent := <-reboots
	if intent.Reason == "" {
		t.Error("an unreadable intent still carries a reason for the console")
	}
}

func TestWatchConsumesARestartIntentAndKeepsWatching(t *testing.T) {
	dir, _, restarts := watchIntents(t)

	want := machine.RestartIntent{Reason: "applying the staged cluster document"}
	if err := machine.WriteRestartIntent(dir, &want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-restarts:
		if got.Reason != want.Reason {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher never delivered the restart intent")
	}

	// The intent was consumed: the file is gone, so the poll can't
	// fire it twice.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if intent, _ := machine.ReadRestartIntent(dir); intent == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the delivered intent should be consumed")
		}
		time.Sleep(time.Millisecond)
	}

	// And the watch is still alive: a second request also arrives.
	if err := machine.WriteRestartIntent(dir, &machine.RestartIntent{Reason: "another change"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-restarts:
		if got.Reason != "another change" {
			t.Errorf("got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watch must outlive a delivered restart")
	}
}

func TestWatchPrefersARebootWhenBothIntentsStand(t *testing.T) {
	// A reboot re-renders everything a restart would, so when both
	// files exist the heavier one wins and the restart file simply
	// burns with the boot.
	dir := t.TempDir()
	if err := machine.WriteRestartIntent(dir, &machine.RestartIntent{Reason: "restart"}); err != nil {
		t.Fatal(err)
	}
	if err := machine.WriteRebootIntent(dir, &machine.RebootIntent{Reason: "reboot"}); err != nil {
		t.Fatal(err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchForOperatorIntents(ctx, dir, time.Millisecond, reboots, restarts); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-reboots:
		if got.Reason != "reboot" {
			t.Errorf("got %+v", got)
		}
	default:
		t.Fatal("the reboot should have been delivered")
	}
	select {
	case got := <-restarts:
		t.Errorf("the restart must lose to the reboot: %+v", got)
	default:
	}
}
