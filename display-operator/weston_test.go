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
// lab's sysfs, a /dev/dri holding one card node, and a weston.ini and
// a mode record in a directory the test owns. It returns the config
// path.
func compositorFixture(t *testing.T) string {
	t.Helper()
	dri := t.TempDir()
	if err := os.WriteFile(filepath.Join(dri, "card1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	config := t.TempDir()
	swapPath(t, &sysRoot, labSysfs(t))
	swapPath(t, &driRoot, dri)
	swapPath(t, &westonConfigPath, filepath.Join(config, "weston", "weston.ini"))
	swapPath(t, &modeRecordPath, filepath.Join(config, "weston", "modes.json"))
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
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), nil)

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

func TestWestonConfigNamesTheModeTheRecordStates(t *testing.T) {
	// The record is the operator's own, and the config is derived from
	// the connector walk plus the record on every write. A connector
	// with no entry keeps the mode the monitor prefers.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), map[string]string{"HDMI-A-2": "1280x720"})

	for _, want := range []string{
		"name=HDMI-A-1\nmode=preferred\napp-ids=hdmi-a-1",
		"name=HDMI-A-2\nmode=1280x720\napp-ids=hdmi-a-2",
		"name=DP-1\nmode=preferred\napp-ids=dp-1",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
}

func TestWestonConfigIgnoresARecordEntryForAConnectorTheCardDoesNotHave(t *testing.T) {
	// The record outlives a monitor and the walk is the truth about
	// what the card has, so an entry with no connector adds no section.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), map[string]string{"HDMI-A-9": "1280x720"})

	if strings.Contains(config, "HDMI-A-9") {
		t.Errorf("the config names a connector the card does not have:\n%s", config)
	}
	if got := strings.Count(config, "[output]"); got != 3 {
		t.Errorf("got %d output sections, want 3:\n%s", got, config)
	}
}

func TestWriteWestonConfigCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston", "weston.ini")

	if err := writeWestonConfig(path, discoverOutputs(labSysfs(t), "card1"), nil); err != nil {
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

func TestDeclareWritesAnEmptyModeRecord(t *testing.T) {
	// The record lives in the pod's own volume beside the config, so a
	// pod that restarts starts with no mode stated and every screen at
	// the mode its monitor prefers. The file exists from the start, so
	// a prepare that reads it before any claim stated a mode reads an
	// empty record rather than a missing file.
	compositorFixture(t)

	declare()

	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 0 {
		t.Fatalf("record = %v", record)
	}
}

func TestDeclareKeepsAModeRecordThatIsAlreadyThere(t *testing.T) {
	// The kubelet runs an init container again when it restarts the
	// pod's containers, and the config it writes must carry whatever
	// mode a claim already stated.
	compositorFixture(t)
	if err := writeModeRecord(modeRecordPath, map[string]string{"HDMI-A-2": "1280x720"}); err != nil {
		t.Fatal(err)
	}

	declare()

	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		t.Fatal(err)
	}
	if record["HDMI-A-2"] != "1280x720" {
		t.Fatalf("record = %v", record)
	}
	written, err := os.ReadFile(westonConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-2\nmode=1280x720") {
		t.Fatalf("the file holds:\n%s", written)
	}
}

// fakeProc builds the part of /proc that the compositor search reads:
// one directory per process, with an exe symlink to the binary it
// runs. The links dangle, which is what a readlink of a real
// /proc/<pid>/exe answers from a container that does not have the
// binary at that path.
func fakeProc(t *testing.T, processes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, binary := range processes {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if binary == "" {
			continue
		}
		if err := os.Symlink(binary, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCompositorProcessesFindsTheCompositor(t *testing.T) {
	// The pod shares one process namespace, so this operator sees the
	// compositor's own process and finds it by the binary it runs.
	// Nothing else in the pod runs weston.
	proc := fakeProc(t, map[string]string{
		"1":    "/usr/bin/display-operator",
		"14":   westonBinary,
		"29":   "/usr/bin/display-operator",
		"self": westonBinary,
	})

	if got := compositorProcesses(proc); !slices.Equal(got, []int{14}) {
		t.Errorf("pids = %v, want [14]", got)
	}
}

func TestCompositorProcessesFindsNoneWhileTheContainerRestarts(t *testing.T) {
	proc := fakeProc(t, map[string]string{
		"1":  "/usr/bin/display-operator",
		"30": "",
	})

	if got := compositorProcesses(proc); len(got) != 0 {
		t.Errorf("pids = %v, want none", got)
	}
}

func TestEndCompositorReportsThatItFoundNone(t *testing.T) {
	// A restart the operator ordered has to be an ordered restart or a
	// failure. A search that found nothing and said nothing would leave
	// a prepare waiting for a mode change that nothing started.
	err := endCompositor(fakeProc(t, map[string]string{"1": "/usr/bin/display-operator"}))
	if err == nil {
		t.Fatal("the search found no compositor and reported no error")
	}
	if !strings.Contains(err.Error(), westonBinary) {
		t.Errorf("error = %q, want it to name %q", err, westonBinary)
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
