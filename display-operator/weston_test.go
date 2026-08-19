package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// compositorFixture points the pod's three roles at one machine: the
// lab's sysfs, a /dev/dri holding one card node, and a weston.ini in a
// directory the test owns. It returns the config path.
func compositorFixture(t *testing.T) string {
	t.Helper()
	dri := t.TempDir()
	if err := os.WriteFile(filepath.Join(dri, "card1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	swapPath(t, &sysRoot, labSysfs(t))
	swapPath(t, &driRoot, dri)
	swapPath(t, &westonConfigPath, filepath.Join(t.TempDir(), "weston", "weston.ini"))
	return westonConfigPath
}

// swapPath points one of the operator's roots at a directory the test
// controls, and puts the real one back when the test ends.
func swapPath(t *testing.T, target *string, value string) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

func TestWestonConfigNamesEachOutputAndItsAppID(t *testing.T) {
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"))

	// The kiosk shell reads app-ids= and matches it against the app-id
	// a client sets. The app-id is the device name, so a claim on
	// hdmi-a-1 receives DISPLAY_APP_ID=hdmi-a-1 and the client that
	// passes it to its toolkit lands on that monitor.
	//
	// DP-1 has nothing on it and gets a section like the others.
	// Weston reads this file once, so the section has to be there
	// before the monitor is.
	for _, want := range []string{
		"shell=kiosk",
		"renderer=gl",
		"require-input=false",
		"idle-time=0",
		"name=HDMI-A-1\nmode=preferred\napp-ids=hdmi-a-1",
		"name=HDMI-A-2\nmode=preferred\napp-ids=hdmi-a-2",
		"name=DP-1\nmode=preferred\napp-ids=dp-1",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
	if got := strings.Count(config, "[output]"); got != 3 {
		t.Errorf("got %d output sections, want 3:\n%s", got, config)
	}
}

func TestWriteWestonConfigCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston", "weston.ini")

	if err := writeWestonConfig(path, discoverOutputs(labSysfs(t), "card1")); err != nil {
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

func TestDeclareWritesTheConfigWhereTheCompositorWaitsForIt(t *testing.T) {
	// The declare container writes the file and the compositor
	// container reads it, so the two roles must name one path.
	path := compositorFixture(t)

	declare()

	if err := waitForFile(context.Background(), westonConfigPath, socketPollInterval); err != nil {
		t.Fatalf("the compositor role waited on %s: %v", westonConfigPath, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "app-ids=hdmi-a-1") {
		t.Fatalf("the file holds:\n%s", written)
	}
	if got := strings.Count(string(written), "[output]"); got != 3 {
		t.Fatalf("got %d output sections, want 3:\n%s", got, written)
	}
}

func TestWestonArgvNamesTheCardAndTheConfig(t *testing.T) {
	argv := westonArgv("card1", "/etc/weston/weston.ini", socketName)

	want := []string{
		westonBinary,
		"--backend=drm",
		"--drm-device=card1",
		"--config=/etc/weston/weston.ini",
		"--socket=wayland-0",
	}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

func TestWestonEnvironmentSetsWhatTheCompositorNeeds(t *testing.T) {
	env := westonEnvironment([]string{"PATH=/usr/bin"}, defaultSocketDir)

	for _, want := range []string{
		// The inherited environment survives, because the container's
		// own settings arrive that way.
		"PATH=/usr/bin",
		// libseat's noop backend is the one a container can use, the
		// shim moves the hotplug subscription to the kernel's netlink
		// group, and weston creates its socket in XDG_RUNTIME_DIR.
		"LIBSEAT_BACKEND=noop",
		"LD_PRELOAD=" + hotplugShim,
		"XDG_RUNTIME_DIR=" + defaultSocketDir,
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env = %v, want %q in it", env, want)
		}
	}
}

func TestWaitForFileReturnsWhenItAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston.ini")
	go func() {
		time.Sleep(2 * socketPollInterval)
		_ = os.WriteFile(path, nil, 0o644)
	}()

	if err := waitForFile(context.Background(), path, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForFileGivesUp(t *testing.T) {
	err := waitForFile(context.Background(), filepath.Join(t.TempDir(), "weston.ini"), 2*socketPollInterval)
	if err == nil {
		t.Fatal("the wait succeeded with no file")
	}
}

// listenOnSocket answers on the compositor's socket until the test
// ends. It leaves the file behind when it closes, which is what a
// compositor killed uncleanly leaves on the host.
func listenOnSocket(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = unixListener.Close() })
	return unixListener
}

// servingSocket is a runtime directory with a compositor answering in
// it. It returns the socket's path.
func servingSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, socketName)
	listenOnSocket(t, path)
	return path
}

// staleSocket is the socket file a dead compositor left behind: the
// path is there and nothing answers on it.
func staleSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, socketName)
	if err := listenOnSocket(t, path).Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the socket file must outlive the listener: %v", err)
	}
	return path
}

func TestCompositorServingConnectsToTheSocket(t *testing.T) {
	dir := t.TempDir()
	if compositorServing(filepath.Join(dir, socketName)) {
		t.Error("a directory with no socket in it reports a compositor")
	}
	if !compositorServing(servingSocket(t, dir)) {
		t.Error("a compositor answers and none is reported")
	}
}

func TestCompositorServingRefusesASocketNothingAnswersOn(t *testing.T) {
	// A compositor that died uncleanly leaves its socket file behind. A
	// check that read the file's presence would call the corpse a
	// compositor, and prepare would deliver a path that refuses every
	// client that connects to it.
	if compositorServing(staleSocket(t, t.TempDir())) {
		t.Error("a socket file with nothing behind it reports a compositor")
	}
}
