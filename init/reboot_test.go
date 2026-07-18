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

	"github.com/liken-sh/liken/machine"
)

// watchIntents arms the watcher over a tempdir with fast polls and
// hands back all three channels.
func watchIntents(t *testing.T) (string, chan machine.RebootIntent, chan machine.RestartIntent, chan machine.ModulesIntent) {
	t.Helper()
	dir := t.TempDir()
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	loads := make(chan machine.ModulesIntent, 1)
	go watchForOperatorIntents(t.Context(), dir, time.Millisecond, reboots, restarts, loads)
	return dir, reboots, restarts, loads
}

func TestWatchDeliversARebootIntent(t *testing.T) {
	dir, reboots, _, _ := watchIntents(t)

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
	if err := watchForOperatorIntents(ctx, dir, time.Millisecond, reboots, restarts, make(chan machine.ModulesIntent, 1)); err != nil {
		t.Fatal(err)
	}
	intent := <-reboots
	if intent.Reason == "" {
		t.Error("an unreadable intent still carries a reason for the console")
	}
}

func TestWatchConsumesARestartIntentAndKeepsWatching(t *testing.T) {
	dir, _, restarts, _ := watchIntents(t)

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

func TestWatchConsumesAModulesIntentAndKeepsWatching(t *testing.T) {
	dir, _, _, loads := watchIntents(t)

	want := machine.ModulesIntent{Reason: "loading the staged spec's added modules", ManifestHash: "abc"}
	if err := machine.WriteModulesIntent(dir, &want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-loads:
		if got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher never delivered the modules intent")
	}

	// Consumed, like the restart intent: the machine lives on, so the
	// file must not fire twice.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if intent, _ := machine.ReadModulesIntent(dir); intent == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the delivered intent should be consumed")
		}
		time.Sleep(time.Millisecond)
	}

	// And the watch is still alive for the next one.
	if err := machine.WriteModulesIntent(dir, &machine.ModulesIntent{Reason: "another edit"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-loads:
		if got.Reason != "another edit" {
			t.Errorf("got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watch must outlive a delivered load")
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
	if err := watchForOperatorIntents(ctx, dir, time.Millisecond, reboots, restarts, make(chan machine.ModulesIntent, 1)); err != nil {
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

func TestWatchHonorsAnUnreadableRestartIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "restart-intent.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- watchForOperatorIntents(ctx, dir, time.Millisecond, reboots, restarts, make(chan machine.ModulesIntent, 1))
	}()
	intent := <-restarts
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if intent.Reason != "an unreadable restart intent" {
		t.Errorf("the fallback reason names the problem: %q", intent.Reason)
	}
}
