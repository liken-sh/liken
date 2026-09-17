package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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

func TestWestonConfigNamesTheShellTheModuleAndEachOutput(t *testing.T) {
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), nil)

	// ivi-shell places nothing on its own, so the module on the
	// modules= line is what shows every surface, and the operator is
	// what tells the module where.
	//
	// DP-1 has nothing on it and gets a section like the others.
	// Weston reads this file once, so the section has to be there
	// before the monitor is.
	for _, want := range []string{
		"shell=ivi-shell.so",
		"modules=liken-layout.so",
		"renderer=gl",
		"require-input=false",
		"require-outputs=none",
		"idle-time=0",
		"name=HDMI-A-1\nmode=preferred",
		"name=HDMI-A-2\nmode=preferred",
		"name=DP-1\nmode=preferred",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
	if got := strings.Count(config, "[output]"); got != 3 {
		t.Errorf("got %d output sections, want 3:\n%s", got, config)
	}
	// ivi-shell reads no app-ids= key. The socket a surface arrives
	// on is what names the claim that drew it.
	if strings.Contains(config, "app-ids=") {
		t.Errorf("the config routes by app-id:\n%s", config)
	}
}

func TestWestonConfigNamesTheModeTheRecordStates(t *testing.T) {
	// The record is the operator's own, and the config is derived from
	// the connector walk plus the record on every write. A connector
	// with no entry keeps the mode the monitor prefers.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), map[string]string{"HDMI-A-2": "1280x720"})

	for _, want := range []string{
		"name=HDMI-A-1\nmode=preferred\n",
		"name=HDMI-A-2\nmode=1280x720\n",
		"name=DP-1\nmode=preferred\n",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
}

func TestWestonConfigScalesAWideOutputByTwo(t *testing.T) {
	// HDMI-A-1 carries the lab's 3840x1600 monitor and prefers that
	// mode, so its section states scale=2. The portable display on
	// HDMI-A-2 prefers 1920x1080 and states no scale, which is weston's
	// default of 1. DP-1 is dark, has no modes, and states none either.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), nil)

	if want := "name=HDMI-A-1\nmode=preferred\nscale=2\n"; !strings.Contains(config, want) {
		t.Errorf("the config does not contain %q:\n%s", want, config)
	}
	if got := strings.Count(config, "scale="); got != 1 {
		t.Errorf("got %d scale lines, want 1:\n%s", got, config)
	}
}

func TestWestonConfigScalesByTheModeTheRecordStates(t *testing.T) {
	// A claim's mode decides the scale, not the monitor's preference: the
	// wide monitor driven at 1920x1080 gets no scale, and the portable
	// display asked for a mode 3840 wide, in the claim's own spelling
	// with a refresh, gets 2.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), map[string]string{
		"HDMI-A-1": "1920x1080",
		"HDMI-A-2": "3840x2160@60",
	})

	for _, want := range []string{
		"name=HDMI-A-1\nmode=1920x1080\n\n",
		"name=HDMI-A-2\nmode=3840x2160@60\nscale=2\n",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("the config does not contain %q:\n%s", want, config)
		}
	}
}

func TestModeWidthReadsBothSpellingsAndRefusesTheRest(t *testing.T) {
	for mode, want := range map[string]int{
		"3840x2160":    3840,
		"3840x2160@60": 3840,
		"1280x720":     1280,
		"preferred":    0,
		"widexhigh":    0,
	} {
		if got := modeWidth(mode); got != want {
			t.Errorf("modeWidth(%q) = %d, want %d", mode, got, want)
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
	if !strings.Contains(string(written), "name=HDMI-A-1") {
		t.Fatalf("the file holds:\n%s", written)
	}
}

func TestDeclareWritesTheConfigWhereTheCompositorWaitsForIt(t *testing.T) {
	// The declare container writes the file and the compositor
	// container reads it, so the two roles must name one path.
	path := compositorFixture(t)

	declare()

	if err := waitForFile(context.Background(), westonConfigPath, filePollInterval); err != nil {
		t.Fatalf("the compositor role waited on %s: %v", westonConfigPath, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-1") {
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
		time.Sleep(2 * filePollInterval)
		_ = os.WriteFile(path, nil, 0o644)
	}()

	if err := waitForFile(context.Background(), path, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForFileGivesUp(t *testing.T) {
	err := waitForFile(context.Background(), filepath.Join(t.TempDir(), "weston.ini"), 2*filePollInterval)
	if err == nil {
		t.Fatal("the wait succeeded with no file")
	}
}

// listenOnSocket binds the compositor's socket until the test ends.
// It leaves the file behind when it closes, which is what a compositor
// killed uncleanly leaves on the host.
//
// It accepts nothing on its own, so a fixture that answers a client
// runs a server over it.
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
//
// The compositor answers the handshake the probe sends, which is what
// a running compositor's socket does.
func servingSocket(t *testing.T, dir string) string {
	t.Helper()
	return westonBenchOn(t, filepath.Join(dir, socketName), nil).path
}

// frozenSocket is the socket of a compositor whose event loop stopped:
// the listener accepts the connect and nothing ever answers on it,
// which is what a weston stopped with SIGSTOP leaves a client.
func frozenSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, socketName)
	listenOnSocket(t, path)
	return path
}

// closingSocket accepts and closes at once, which is what the socket
// of a compositor that is exiting does.
func closingSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, socketName)
	listener := listenOnSocket(t, path)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	return path
}

// missingSocket is a runtime directory in which the compositor's
// container never created its socket.
func missingSocket(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, socketName)
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

func TestProbeCompositorReadsWhatTheSocketAnswers(t *testing.T) {
	// The bound is set for the whole test, so the frozen case waits
	// 200 ms once in place of the 2 s the operator waits.
	compositorReplyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { compositorReplyTimeout = 2 * time.Second })

	cases := []struct {
		name    string
		socket  func(t *testing.T, dir string) string
		serving bool
		reason  string
	}{
		{"a compositor that answers", servingSocket, true, CompositorServingReason},
		// The compositor's container has not created its socket yet.
		{"no socket at all", missingSocket, false, CompositorDownReason},
		// A compositor that died uncleanly leaves its socket file
		// behind. A check that read the file's presence would call the
		// corpse a compositor, and prepare would deliver a path that
		// refuses every client that connects to it.
		{"the socket a dead compositor left", staleSocket, false, CompositorDownReason},
		// The case the testbed drill found: the socket accepts, and
		// the process behind it runs no event loop.
		{"a socket that accepts and never answers", frozenSocket, false, CompositorHungReason},
		{"a socket that closes on the handshake", closingSocket, false, CompositorDownReason},
	}
	for _, drill := range cases {
		t.Run(drill.name, func(t *testing.T) {
			path := drill.socket(t, t.TempDir())

			probe := probeCompositor(path)

			if probe.serving != drill.serving || probe.reason != drill.reason {
				t.Errorf("the probe answers serving=%v/%s, want serving=%v/%s",
					probe.serving, probe.reason, drill.serving, drill.reason)
			}
			if !probe.serving && probe.detail == "" {
				t.Error("a compositor that serves nobody carries no words of its own")
			}
			if compositorServing(path) != drill.serving {
				t.Errorf("compositorServing = %v, want %v", compositorServing(path), drill.serving)
			}
		})
	}
}

// podWithRestarts is the pod this operator runs in, as the API server
// answers for it, with the compositor's sidecar at the named restart
// count.
func podWithRestarts(t *testing.T, counts *int) *Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/liken-system/pods/display-operator-abcde" {
			t.Errorf("the reader asked for %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pod := Pod{
			Metadata: PodMeta{Name: "display-operator-abcde", Namespace: "liken-system"},
			Status: PodStatus{
				// The compositor is a native sidecar, so the kubelet
				// reports it among the init containers.
				InitContainerStatuses: []ContainerStatus{
					{Name: "declare", RestartCount: 0},
					{Name: compositorMode, RestartCount: *counts},
				},
				ContainerStatuses: []ContainerStatus{{Name: "operator", RestartCount: 0}},
			},
		}
		_ = json.NewEncoder(w).Encode(pod)
	}))
}

func TestTheSidecarsRestartCountIsReadAsGrowth(t *testing.T) {
	counts := 2
	restarts := newWestonRestarts(podWithRestarts(t, &counts), "liken-system", "display-operator-abcde")

	cases := []struct {
		name   string
		count  int
		growth int
	}{
		// The operator's own container restarted under a compositor
		// the kubelet had already started twice more, and none of
		// those restarts happened while this process ran.
		{"the first read is the baseline", 2, 0},
		{"a compositor that did not restart", 2, 0},
		{"one restart", 3, 1},
		{"two restarts between two reads", 5, 2},
		// The pod was replaced, so the kubelet counts from zero again.
		{"a count that went backwards", 0, 0},
		{"the first restart of the new pod", 1, 1},
	}
	for _, drill := range cases {
		t.Run(drill.name, func(t *testing.T) {
			counts = drill.count

			growth, err := restarts.growth()

			if err != nil {
				t.Fatal(err)
			}
			if growth != drill.growth {
				t.Errorf("the reader counted %d restarts, want %d", growth, drill.growth)
			}
		})
	}
}

func TestAPodTheAPIServerCannotAnswerForCountsNothing(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	restarts := newWestonRestarts(client, "liken-system", "display-operator-abcde")

	growth, err := restarts.growth()

	if err == nil {
		t.Fatal("a pod read that failed reported no error")
	}
	if growth != 0 {
		t.Errorf("a pod read that failed counted %d restarts", growth)
	}
}

// An operator run by hand has no pod of its own to read.
func TestAnOperatorWithNoPodOfItsOwnCountsNothing(t *testing.T) {
	restarts := newWestonRestarts(nil, "", "")

	growth, err := restarts.growth()

	if err != nil || growth != 0 {
		t.Errorf("a reader with no pod answered %d/%v", growth, err)
	}
}

func TestTheHungRepairWaitsForTheBoundAndRunsOnce(t *testing.T) {
	compositorHungLimit = 10 * time.Second
	start := time.Unix(0, 0).UTC()
	hung := compositorLiveness{reason: CompositorHungReason, detail: "i/o timeout"}
	down := compositorLiveness{reason: CompositorDownReason, detail: "connection refused"}
	serving := compositorLiveness{serving: true, reason: CompositorServingReason}

	steps := []struct {
		name    string
		live    compositorLiveness
		seconds int
		due     bool
		killed  bool
	}{
		{name: "the first reading of the freeze", live: hung, seconds: 0},
		// A freeze shorter than the bound is a compositor under load,
		// and the operator ends nothing.
		{name: "inside the bound", live: hung, seconds: 9},
		{name: "the compositor answered again", live: serving, seconds: 10},
		{name: "a second freeze starts its own clock", live: hung, seconds: 11},
		{name: "inside the second bound", live: hung, seconds: 20},
		{name: "the bound runs out", live: hung, seconds: 21, due: true, killed: true},
		// The kill ran, so the rest of this outage orders no second
		// kill.
		{name: "still frozen after the kill", live: hung, seconds: 22},
		{name: "the process died and the socket refuses", live: down, seconds: 23},
		{name: "a freeze in a later outage", live: hung, seconds: 24},
		{name: "that outage reaches the bound", live: hung, seconds: 34, due: true, killed: true},
	}
	repair := &hungCompositor{}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			due := repair.due(step.live, start.Add(time.Duration(step.seconds)*time.Second))

			if due != step.due {
				t.Errorf("the repair is due=%v, want %v", due, step.due)
			}
			if step.killed {
				repair.done()
			}
		})
	}
}

func TestTheWatchEndsACompositorThatAnswersNothing(t *testing.T) {
	compositorReplyTimeout = 100 * time.Millisecond
	compositorHungLimit = 0
	ctx, cancel := context.WithCancel(context.Background())

	var kills atomic.Int64
	wakes := watchSocket(ctx, frozenSocket(t, t.TempDir()), func() error {
		kills.Add(1)
		return nil
	})
	// The watch reads both bounds on every tick, so the test waits
	// for the watch to end before it restores them.
	t.Cleanup(func() {
		cancel()
		for range wakes {
		}
		compositorReplyTimeout = 2 * time.Second
		compositorHungLimit = 10 * time.Second
	})

	waitUntil(t, "the operator ends the compositor that answers nothing", func() bool {
		return kills.Load() == 1
	})
	// The compositor stays frozen, and one outage costs one kill.
	time.Sleep(2 * socketWatchInterval)
	if got := kills.Load(); got != 1 {
		t.Errorf("the watch ended the compositor %d times in one outage", got)
	}
}
