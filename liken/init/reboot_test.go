package main

// The shutdown sequence in rebootMachine runs only as PID 1, so these
// tests cover only the intent watch, and the QEMU harness tests the
// shutdown.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liken-sh/liken/liken/machine"
)

// watchIntents starts the watch on a temporary directory and returns
// the directory and the channels the watch sends on.
func watchIntents(t *testing.T) (string, chan machine.RebootIntent, chan machine.RestartIntent, chan machine.ModulesIntent) {
	t.Helper()
	dir := t.TempDir()
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	loads := make(chan machine.ModulesIntent, 1)
	go watchForOperatorIntents(t.Context(), dir, reboots, restarts, loads)
	return dir, reboots, restarts, loads
}

func TestWatchReturnsAnErrorWhenTheDirectoryIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	loads := make(chan machine.ModulesIntent, 1)

	done := make(chan error, 1)
	go func() {
		done <- watchForOperatorIntents(t.Context(), dir, reboots, restarts, loads)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("watching a missing directory returned nil, want an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watching a missing directory did not return within 2 seconds, want it to return")
	}
}

func TestWatchDeliversARebootIntent(t *testing.T) {
	dir, reboots, _, _ := watchIntents(t)

	select {
	case got := <-reboots:
		t.Fatalf("received %+v before any intent was written, want none", got)
	case <-time.After(20 * time.Millisecond):
	}

	want := machine.RebootIntent{Reason: "applying the staged spec", ManifestHash: "abc"}
	if err := machine.WriteRebootIntent(dir, &want); err != nil {
		t.Fatalf("writing the reboot intent: %v", err)
	}
	select {
	case got := <-reboots:
		if got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
			t.Errorf("the intent is %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reboot intent arrived within 2 seconds of writing it, want one")
	}
}

func TestWatchDeliversAnIntentOnTheEventPath(t *testing.T) {
	// An intent written after the first scan can arrive only through
	// an inotify event, because the watch has no timer.
	dir, _, restarts, _ := watchIntents(t)

	select {
	case got := <-restarts:
		t.Fatalf("received %+v before any intent was written, want none", got)
	case <-time.After(50 * time.Millisecond):
	}

	want := machine.RestartIntent{Reason: "applying the staged cluster document"}
	if err := machine.WriteRestartIntent(dir, &want); err != nil {
		t.Fatalf("writing the restart intent: %v", err)
	}
	select {
	case got := <-restarts:
		if got.Reason != want.Reason {
			t.Errorf("the intent is %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no restart intent arrived within 2 seconds of writing it after the first scan, want one")
	}
}

func TestWatchHonorsAnUnreadableRebootIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reboot-intent.yaml"), []byte("{garbled"), 0o644); err != nil {
		t.Fatalf("writing the unreadable reboot intent file: %v", err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchForOperatorIntents(ctx, dir, reboots, restarts, make(chan machine.ModulesIntent, 1)); err != nil {
		t.Fatalf("watching the intent directory: %v", err)
	}
	intent := <-reboots
	if intent.Reason == "" {
		t.Error("the reason of the reboot intent is empty, want a placeholder reason for the console")
	}
}

func TestWatchConsumesARestartIntentAndKeepsWatching(t *testing.T) {
	dir, _, restarts, _ := watchIntents(t)

	want := machine.RestartIntent{Reason: "applying the staged cluster document"}
	if err := machine.WriteRestartIntent(dir, &want); err != nil {
		t.Fatalf("writing the restart intent: %v", err)
	}
	select {
	case got := <-restarts:
		if got.Reason != want.Reason {
			t.Errorf("the intent is %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no restart intent arrived within 2 seconds of writing it, want one")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if intent, _ := machine.ReadRestartIntent(dir); intent == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the intent file still exists 2 seconds after the watch sent it, want it deleted")
		}
		time.Sleep(time.Millisecond)
	}

	if err := machine.WriteRestartIntent(dir, &machine.RestartIntent{Reason: "another change"}); err != nil {
		t.Fatalf("writing the second restart intent: %v", err)
	}
	select {
	case got := <-restarts:
		if got.Reason != "another change" {
			t.Errorf("the intent is %+v, want reason \"another change\"", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no second restart intent arrived, want the watch to continue after the first")
	}
}

func TestWatchConsumesAModulesIntentAndKeepsWatching(t *testing.T) {
	dir, _, _, loads := watchIntents(t)

	want := machine.ModulesIntent{Reason: "loading the staged spec's added modules", ManifestHash: "abc"}
	if err := machine.WriteModulesIntent(dir, &want); err != nil {
		t.Fatalf("writing the modules intent: %v", err)
	}
	select {
	case got := <-loads:
		if got.Reason != want.Reason || got.ManifestHash != want.ManifestHash {
			t.Errorf("the intent is %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no modules intent arrived within 2 seconds of writing it, want one")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if intent, _ := machine.ReadModulesIntent(dir); intent == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the intent file still exists 2 seconds after the watch sent it, want it deleted")
		}
		time.Sleep(time.Millisecond)
	}

	if err := machine.WriteModulesIntent(dir, &machine.ModulesIntent{Reason: "another edit"}); err != nil {
		t.Fatalf("writing the second modules intent: %v", err)
	}
	select {
	case got := <-loads:
		if got.Reason != "another edit" {
			t.Errorf("the intent is %+v, want reason \"another edit\"", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no second modules intent arrived, want the watch to continue after the first")
	}
}

func TestWatchPrefersARebootWhenBothIntentsExist(t *testing.T) {
	dir := t.TempDir()
	if err := machine.WriteRestartIntent(dir, &machine.RestartIntent{Reason: "restart"}); err != nil {
		t.Fatalf("writing the restart intent: %v", err)
	}
	if err := machine.WriteRebootIntent(dir, &machine.RebootIntent{Reason: "reboot"}); err != nil {
		t.Fatalf("writing the reboot intent: %v", err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := watchForOperatorIntents(ctx, dir, reboots, restarts, make(chan machine.ModulesIntent, 1)); err != nil {
		t.Fatalf("watching the intent directory: %v", err)
	}
	select {
	case got := <-reboots:
		if got.Reason != "reboot" {
			t.Errorf("the reboot intent is %+v, want reason \"reboot\"", got)
		}
	default:
		t.Fatal("no reboot intent was sent, want one")
	}
	select {
	case got := <-restarts:
		t.Errorf("a restart intent %+v was sent, want only the reboot", got)
	default:
	}
}

func TestWatchHonorsAnUnreadableRestartIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "restart-intent.yaml"), []byte("{not yaml"), 0o644); err != nil {
		t.Fatalf("writing the unreadable restart intent file: %v", err)
	}
	reboots := make(chan machine.RebootIntent, 1)
	restarts := make(chan machine.RestartIntent, 1)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- watchForOperatorIntents(ctx, dir, reboots, restarts, make(chan machine.ModulesIntent, 1))
	}()
	intent := <-restarts
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watching the intent directory: %v", err)
	}
	if intent.Reason != "an unreadable restart intent" {
		t.Errorf("the restart reason is %q, want \"an unreadable restart intent\"", intent.Reason)
	}
}

// Returning nil would tell the machine plane not to start the watch
// again.
func TestTheIntentWatchEndsWithAnErrorWhenItsWatchStops(t *testing.T) {
	saved := watchIntentDir
	t.Cleanup(func() { watchIntentDir = saved })
	watchIntentDir = func(context.Context, string) (<-chan struct{}, error) {
		stopped := make(chan struct{})
		close(stopped)
		return stopped, nil
	}

	err := watchForOperatorIntents(t.Context(), t.TempDir(),
		make(chan machine.RebootIntent, 1), make(chan machine.RestartIntent, 1), make(chan machine.ModulesIntent, 1))

	if err == nil {
		t.Error("the intent watch returned nil after its watch ended, want an error so the machine plane starts it again")
	}
}

// unremovableIntent puts a non-empty directory where an intent file
// belongs. Reading it fails, and removing it fails even for root.
func unremovableIntent(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name, "stuck"), 0o755); err != nil {
		t.Fatalf("creating the undeletable intent directory: %v", err)
	}
}

func TestScanIntentsDeliversIntentsItCannotConsume(t *testing.T) {
	dir := t.TempDir()
	unremovableIntent(t, dir, "restart-intent.yaml")
	unremovableIntent(t, dir, "modules-intent.yaml")
	restarts := make(chan machine.RestartIntent, 1)
	loads := make(chan machine.ModulesIntent, 1)

	ended := scanIntents(dir, make(chan machine.RebootIntent, 1), restarts, loads)

	if ended || len(restarts) != 1 || len(loads) != 1 {
		t.Fatalf("the scan returned %v with %d restarts and %d loads, want false with one of each", ended, len(restarts), len(loads))
	}
	if restart, load := <-restarts, <-loads; restart.Reason != "an unreadable restart intent" || load.Reason != "an unreadable modules intent" {
		t.Errorf("the reasons are %q and %q, want the placeholders for unreadable restart and modules intents", restart.Reason, load.Reason)
	}
}
