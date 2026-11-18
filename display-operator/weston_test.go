package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// compositorFixture points declare and the compositor role at the lab's
// sysfs, a /dev/dri with one card node, and a weston.ini and a mode
// record in a temporary directory. It returns the config path.
func compositorFixture(t *testing.T) string {
	t.Helper()
	dri := t.TempDir()
	if err := os.WriteFile(filepath.Join(dri, "card1"), nil, 0o644); err != nil {
		t.Fatalf("creating the card file: %v", err)
	}
	config := t.TempDir()
	swapPath(t, &sysRoot, labSysfs(t))
	swapPath(t, &driRoot, dri)
	swapPath(t, &westonConfigPath, filepath.Join(config, "weston", "weston.ini"))
	swapPath(t, &modeRecordPath, filepath.Join(config, "weston", "modes.json"))
	// No Display sets a declared mode unless the test sets one.
	swapDisplays(t, nil)
	return westonConfigPath
}

// swapDisplays makes declare read displays and not call the API
// server.
func swapDisplays(t *testing.T, displays []Display) {
	t.Helper()
	previous := declaredDisplays
	declaredDisplays = func() []Display { return displays }
	t.Cleanup(func() { declaredDisplays = previous })
}

// swapPath sets one of the operator's root paths to value and restores
// the old value when the test ends.
func swapPath(t *testing.T, target *string, value string) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

func TestWestonConfigHasTheShellTheModuleAndASectionForEachOutput(t *testing.T) {
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), nil)

	// DP-1 has no monitor and still gets a section.
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
	// ivi-shell has no app-ids= key. The socket that a surface connects
	// on identifies its claim.
	if strings.Contains(config, "app-ids=") {
		t.Errorf("the config routes by app-id:\n%s", config)
	}
}

func TestWestonConfigUsesTheModeInTheRecord(t *testing.T) {
	// A connector with no entry in the record keeps its monitor's
	// preferred mode.
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
	// HDMI-A-1's monitor prefers 3840x1600, so its section has scale=2.
	// HDMI-A-2's monitor prefers 1920x1080, and DP-1 has no monitor, so
	// neither has a scale line, and weston uses its default of 1.
	config := westonConfig(discoverOutputs(labSysfs(t), "card1"), nil)

	if want := "name=HDMI-A-1\nmode=preferred\nscale=2\n"; !strings.Contains(config, want) {
		t.Errorf("the config does not contain %q:\n%s", want, config)
	}
	if got := strings.Count(config, "scale="); got != 1 {
		t.Errorf("got %d scale lines, want 1:\n%s", got, config)
	}
}

func TestWestonConfigScalesByTheModeInTheRecord(t *testing.T) {
	// The mode in the record decides the scale, not the monitor's
	// preferred mode.
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

func TestModeWidthReadsBothFormsAndReturnsZeroForOtherNames(t *testing.T) {
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

func TestWestonConfigIgnoresARecordEntryForAMissingConnector(t *testing.T) {
	// The connector walk decides which sections exist, so a record entry
	// for a connector that the card does not have adds none.
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
		t.Fatalf("writing the weston config: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the weston config: %v", err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-1") {
		t.Fatalf("the weston config is:\n%s\nwant an output section for HDMI-A-1", written)
	}
}

func TestDeclareWritesTheConfigAtThePathTheCompositorWaitsOn(t *testing.T) {
	// The declare container writes the file and the compositor
	// container reads it, so both roles must use the same path.
	path := compositorFixture(t)

	declare()

	if err := waitForFile(context.Background(), westonConfigPath, 100*time.Millisecond); err != nil {
		t.Fatalf("the compositor role waited on %s: %v", westonConfigPath, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the weston config: %v", err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-1") {
		t.Fatalf("the file holds:\n%s", written)
	}
	if got := strings.Count(string(written), "[output]"); got != 3 {
		t.Fatalf("got %d output sections, want 3:\n%s", got, written)
	}
}

func TestDeclareWritesAnEmptyModeRecord(t *testing.T) {
	// A prepare that reads the record before any claim sets a mode gets
	// an empty record and not a missing file.
	compositorFixture(t)

	declare()

	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		t.Fatalf("reading the mode record: %v", err)
	}
	if len(record) != 0 {
		t.Fatalf("the mode record is %v, want it empty", record)
	}
}

func TestDeclareKeepsAnExistingModeRecordAndWritesItsModesToTheConfig(t *testing.T) {
	// The kubelet runs declare again when it restarts the pod's
	// containers, and the config must keep the modes that claims set.
	compositorFixture(t)
	if err := writeModeRecord(modeRecordPath, map[string]string{"HDMI-A-2": "1280x720"}); err != nil {
		t.Fatalf("writing the mode record: %v", err)
	}

	declare()

	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		t.Fatalf("reading the mode record: %v", err)
	}
	if record["HDMI-A-2"] != "1280x720" {
		t.Fatalf("the mode record is %v, want HDMI-A-2 set to 1280x720", record)
	}
	written, err := os.ReadFile(westonConfigPath)
	if err != nil {
		t.Fatalf("reading the weston config: %v", err)
	}
	if !strings.Contains(string(written), "name=HDMI-A-2\nmode=1280x720") {
		t.Fatalf("the weston config is:\n%s\nwant HDMI-A-2 with mode 1280x720", written)
	}
}

// fakeProc builds the part of /proc that compositorProcesses reads: a
// directory for each pid with an exe symlink to its binary. The links
// dangle, as /proc/<pid>/exe does for a binary that this container does
// not have. An empty binary makes a process with no exe link.
func fakeProc(t *testing.T, processes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, binary := range processes {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the process directory: %v", err)
		}
		if binary == "" {
			continue
		}
		if err := os.Symlink(binary, filepath.Join(dir, "exe")); err != nil {
			t.Fatalf("linking the exe of the process: %v", err)
		}
	}
	return root
}

func TestCompositorProcessesFindsTheCompositor(t *testing.T) {
	// The self entry is not a process and must not be listed.
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

func TestCompositorProcessesReturnsNoneWhenNoProcessHasAnExeLink(t *testing.T) {
	proc := fakeProc(t, map[string]string{
		"1":  "/usr/bin/display-operator",
		"30": "",
	})

	if got := compositorProcesses(proc); len(got) != 0 {
		t.Errorf("pids = %v, want none", got)
	}
}

func TestWestonArgvHasTheCardTheConfigAndTheSocket(t *testing.T) {
	argv := westonArgv("card1", "/etc/weston/weston.ini", socketName, "")

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

// The requested scopes follow log in the flag, so weston's own lines
// stay in the container log.
func TestWestonArgvListsTheRequestedLogScopesAfterLog(t *testing.T) {
	cases := []struct {
		name   string
		scopes string
		want   string
	}{
		{name: "one scope", scopes: "drm-backend", want: "--logger-scopes=log,drm-backend"},
		{name: "two scopes", scopes: "drm-backend,timeline", want: "--logger-scopes=log,drm-backend,timeline"},
		{name: "spaces and empty entries", scopes: " drm-backend, ,timeline ,", want: "--logger-scopes=log,drm-backend,timeline"},
		{name: "log requested again", scopes: "log,drm-backend", want: "--logger-scopes=log,drm-backend"},
		{name: "a scope requested twice", scopes: "drm-backend,drm-backend", want: "--logger-scopes=log,drm-backend"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := westonArgv("card1", "/etc/weston/weston.ini", socketName, c.scopes)
			if argv[len(argv)-1] != c.want {
				t.Fatalf("argv = %v, want it to end with %q", argv, c.want)
			}
		})
	}
}

// An empty value, or one that has only log, leaves weston on its
// default logger, which already writes the log scope.
func TestWestonArgvAddsNoLoggerFlagWithoutAnExtraScope(t *testing.T) {
	cases := []struct {
		name   string
		scopes string
	}{
		{name: "unset", scopes: ""},
		{name: "blank", scopes: " , "},
		{name: "log alone", scopes: "log"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := westonArgv("card1", "/etc/weston/weston.ini", socketName, c.scopes)
			for _, arg := range argv {
				if strings.HasPrefix(arg, "--logger-scopes") {
					t.Fatalf("argv = %v, want no --logger-scopes", argv)
				}
			}
		})
	}
}

func TestWestonEnvironmentAddsTheSettingsWestonNeeds(t *testing.T) {
	env := westonEnvironment([]string{"PATH=/usr/bin"}, defaultSocketDir)

	for _, want := range []string{
		"PATH=/usr/bin",
		"LIBSEAT_BACKEND=noop",
		"LD_PRELOAD=" + hotplugShim,
		"XDG_RUNTIME_DIR=" + defaultSocketDir,
	} {
		if !slices.Contains(env, want) {
			t.Errorf("env = %v, want %q in it", env, want)
		}
	}
}

// The timeout is shorter than fileWaitFallback, so only the inotify
// event for the file can end the wait in time.
func TestWaitForFileReturnsWhenTheFileAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weston.ini")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, nil, 0o644)
	}()

	if err := waitForFile(context.Background(), path, fileWaitFallback/2); err != nil {
		t.Fatalf("waiting for the file: %v", err)
	}
}

// While the file's directory does not exist, no inotify event can come,
// so the wait finds the file at a check after fileWaitFallback.
func TestWaitForFileReturnsWhenTheDirectoryAppearsLater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "weston")
	path := filepath.Join(dir, "weston.ini")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.Mkdir(dir, 0o755)
		_ = os.WriteFile(path, nil, 0o644)
	}()

	if err := waitForFile(context.Background(), path, 3*fileWaitFallback); err != nil {
		t.Fatalf("waiting for the file: %v", err)
	}
}

func TestWaitForFileFailsAfterTheTimeoutWhenNoFileAppears(t *testing.T) {
	err := waitForFile(context.Background(), filepath.Join(t.TempDir(), "weston.ini"), 100*time.Millisecond)
	if err == nil {
		t.Fatal("waitForFile returned no error, want an error because no file appeared")
	}
}

// listenOnSocket listens on path until the test ends, and leaves the
// socket file in place when it closes, as a killed compositor does. It
// accepts no connections.
func listenOnSocket(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on the socket: %v", err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = unixListener.Close() })
	return unixListener
}

// servingSocket starts a fake compositor in dir that replies to
// wl_display.sync, and returns the socket's path.
func servingSocket(t *testing.T, dir string) string {
	t.Helper()
	return westonBenchOn(t, filepath.Join(dir, socketName), nil).path
}

// closingSocket accepts each connection and closes it at once, as the
// socket of an exiting compositor does.
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

// missingSocket returns a socket path in dir where nothing created a
// socket.
func missingSocket(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, socketName)
}

// staleSocket returns the path of a socket file with no listener, as a
// dead compositor leaves it.
func staleSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, socketName)
	if err := listenOnSocket(t, path).Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the socket file must outlive the listener: %v", err)
	}
	return path
}

func TestProbeCompositorReportsServingOrTheReasonItFailed(t *testing.T) {
	cases := []struct {
		name    string
		socket  func(t *testing.T, dir string) string
		serving bool
		reason  string
	}{
		{"a compositor that replies", servingSocket, true, CompositorServingReason},
		// The compositor's container has not created its socket.
		{"no socket at all", missingSocket, false, CompositorDownReason},
		// A killed compositor leaves its socket file behind. A check
		// that only looked for the file would report it as serving.
		{"the socket file a dead compositor left", staleSocket, false, CompositorDownReason},
		{"a socket that closes on the handshake", closingSocket, false, CompositorDownReason},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := c.socket(t, t.TempDir())

			probe := probeCompositor(path)

			if probe.serving != c.serving || probe.reason != c.reason {
				t.Errorf("the probe returned serving=%v/%s, want serving=%v/%s",
					probe.serving, probe.reason, c.serving, c.reason)
			}
			if !probe.serving && probe.detail == "" {
				t.Error("a failed probe has an empty detail, want the socket error")
			}
			if compositorServing(path) != c.serving {
				t.Errorf("compositorServing = %v, want %v", compositorServing(path), c.serving)
			}
		})
	}
}

// The fake compositor reads the request and never replies, as a weston
// stopped with SIGSTOP does.
func TestProbeReportsHungWhenTheCompositorAcceptsAndNeverAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		probe, compositor := net.Pipe()
		t.Cleanup(func() {
			_ = probe.Close()
			_ = compositor.Close()
		})
		go func() { _, _ = io.Copy(io.Discard, compositor) }()
		start := time.Now()

		live := probeConnection(probe)

		if live.serving || live.reason != CompositorHungReason || live.detail == "" {
			t.Errorf("the probe returned %+v, want not serving, %s, and a detail", live, CompositorHungReason)
		}
		if waited := time.Since(start); waited != compositorReplyTimeout {
			t.Errorf("the probe waited %v for a reply, want %v", waited, compositorReplyTimeout)
		}
	})
}

// podWithRestarts returns a client for a fake API server that serves
// the operator's pod, with the compositor sidecar's restart count read
// from *counts on each request.
func podWithRestarts(t *testing.T, counts *int) *apiclient.Client {
	t.Helper()
	return testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/liken-system/pods/display-operator-abcde" {
			t.Errorf("the pod read requested %s, want the operator pod", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pod := Pod{
			Metadata: PodMeta{Name: "display-operator-abcde", Namespace: "liken-system"},
			Status: PodStatus{
				// The kubelet reports a native sidecar with the init
				// containers.
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

func TestGrowthIsTheIncreaseInTheSidecarsRestartCount(t *testing.T) {
	counts := 2
	restarts := newWestonRestarts(podWithRestarts(t, &counts), "liken-system", "display-operator-abcde")

	cases := []struct {
		name   string
		count  int
		growth int
	}{
		// The compositor had restarted twice before this process
		// started, so those restarts do not count.
		{"the first read is the baseline", 2, 0},
		{"a compositor that did not restart", 2, 0},
		{"one restart", 3, 1},
		{"two restarts between two reads", 5, 2},
		// The pod was replaced, so the count starts from zero.
		{"a count that went backwards", 0, 0},
		{"the first restart of the new pod", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			counts = c.count

			growth, err := restarts.growth()

			if err != nil {
				t.Fatalf("reading the restart growth: %v", err)
			}
			if growth != c.growth {
				t.Errorf("growth = %d restarts, want %d", growth, c.growth)
			}
		})
	}
}

func TestGrowthReturnsAnErrorAndZeroWhenThePodReadFails(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	restarts := newWestonRestarts(client, "liken-system", "display-operator-abcde")

	growth, err := restarts.growth()

	if err == nil {
		t.Fatal("growth returned no error, want an error because the pod read failed")
	}
	if growth != 0 {
		t.Errorf("growth = %d after a failed pod read, want 0", growth)
	}
}

// An operator that a person runs by hand has no pod to read.
func TestGrowthIsZeroWhenThereIsNoPodToRead(t *testing.T) {
	restarts := newWestonRestarts(nil, "", "")

	growth, err := restarts.growth()

	if err != nil || growth != 0 {
		t.Errorf("growth = %d, err = %v, want 0 and no error", growth, err)
	}
}

func TestHungRepairIsDueAfterTheLimitAndOnlyOncePerOutage(t *testing.T) {
	start := time.Unix(0, 0).UTC()

	steps := []struct {
		name    string
		live    compositorLiveness
		seconds int
		due     bool
		killed  bool
	}{
		{name: "the first Hung result", live: probeHung, seconds: 0},
		// A hang shorter than the limit is a compositor under load, and
		// the operator does not kill it.
		{name: "before the limit", live: probeHung, seconds: 9},
		{name: "the compositor replies again", live: probeServing, seconds: 10},
		{name: "a second hang starts a new count", live: probeHung, seconds: 11},
		{name: "before the second limit", live: probeHung, seconds: 20},
		{name: "the limit passes", live: probeHung, seconds: 21, due: true, killed: true},
		// The compositor was killed, so the rest of this outage has no
		// second kill.
		{name: "still Hung after the kill", live: probeHung, seconds: 22},
		{name: "the process died and the socket refuses", live: probeDown, seconds: 23},
		{name: "a hang in a later outage", live: probeHung, seconds: 24},
		{name: "that outage reaches the limit", live: probeHung, seconds: 34, due: true, killed: true},
	}
	repair := &hungCompositor{}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			due := repair.due(step.live, start.Add(time.Duration(step.seconds)*time.Second))

			if due != step.due {
				t.Errorf("due = %v, want %v", due, step.due)
			}
			if step.killed {
				repair.done()
			}
		})
	}
}

func TestWatchKillsAHungCompositorOncePerOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var kills []time.Duration
		compositor := &scriptedProbe{live: probeHung}
		watchSocket(t.Context(), filepath.Join(t.TempDir(), socketName), compositor.probe, func() error {
			kills = append(kills, time.Since(start))
			return nil
		})

		time.Sleep(3 * compositorHungLimit)
		synctest.Wait()

		if len(kills) != 1 || kills[0] != compositorHungLimit {
			t.Errorf("the watch killed the compositor at %v, want once at %v", kills, compositorHungLimit)
		}
	})
}
