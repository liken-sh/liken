package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWestonConfigNamesEachOutputAndItsAppID(t *testing.T) {
	config := westonConfig(connected(discoverOutputs(labSysfs(t), "card1")))

	// The kiosk shell reads app-ids= and matches it against the app-id
	// a client sets. The app-id is the device name, so a claim on
	// hdmi-a-1 receives DISPLAY_APP_ID=hdmi-a-1 and the client that
	// passes it to its toolkit lands on that monitor.
	for _, want := range []string{
		"shell=kiosk",
		"renderer=gl",
		"require-input=false",
		"idle-time=0",
		"name=HDMI-A-1\nmode=preferred\napp-ids=hdmi-a-1",
		"name=HDMI-A-2\nmode=preferred\napp-ids=hdmi-a-2",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
	// The connector with nothing on it gets no section. The compositor
	// drives the monitors that are there.
	if strings.Contains(config, "DP-1") {
		t.Errorf("an output with no monitor got a section:\n%s", config)
	}
	if got := strings.Count(config, "[output]"); got != 2 {
		t.Errorf("got %d output sections, want 2:\n%s", got, config)
	}
}

func TestWriteWestonConfigCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston", "weston.ini")

	if err := writeWestonConfig(path, connected(discoverOutputs(labSysfs(t), "card1"))); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "app-ids=hdmi-a-1") {
		t.Fatalf("the file holds:\n%s", written)
	}
}

func TestWaitForSocketReturnsWhenTheSocketAppears(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wayland-0")
	go func() {
		time.Sleep(2 * socketPollInterval)
		_ = os.WriteFile(path, nil, 0o644)
	}()

	if err := waitForSocket(context.Background(), path, time.Second, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForSocketFailsWhenTheCompositorExits(t *testing.T) {
	// A machine with no monitor plugged in is the case that meets
	// this: the compositor refuses to start, and the wait must report
	// that now rather than after the whole timeout.
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")

	err := waitForSocket(context.Background(), filepath.Join(t.TempDir(), "wayland-0"), time.Minute, exited)
	if err == nil {
		t.Fatal("the wait succeeded with no socket")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitForSocketGivesUp(t *testing.T) {
	err := waitForSocket(context.Background(), filepath.Join(t.TempDir(), "wayland-0"), 2*socketPollInterval, nil)
	if err == nil {
		t.Fatal("the wait succeeded with no socket")
	}
}
