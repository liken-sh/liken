package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestMPVArguments(t *testing.T) {
	useSocket(t, "/tmp/test-mpv.sock")

	cases := []struct {
		name          string
		applicationID string
		items         []string
		want          []string
	}{
		{
			name:  "one film with no display claim",
			items: []string{"/media/0/film.mkv"},
			want: []string{
				"--vo=gpu", "--gpu-context=wayland", "--hwdec=vaapi", "--fullscreen",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--", "/media/0/film.mkv",
			},
		},
		{
			name:          "the display claim names the surface",
			applicationID: "display-0",
			items:         []string{"https://media.example.net/one.mkv", "/media/0/two.mkv"},
			want: []string{
				"--vo=gpu", "--gpu-context=wayland", "--hwdec=vaapi", "--fullscreen",
				"--ao=pipewire", "--input-ipc-server=/tmp/test-mpv.sock",
				"--wayland-app-id=display-0",
				"--", "https://media.example.net/one.mkv", "/media/0/two.mkv",
			},
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Setenv(displayAppIDVariable, each.applicationID)
			mustMatchAll(t, mpvArguments(each.items), each.want)
		})
	}
}

func TestFormatPosition(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{seconds: 0, want: "0:00:00"},
		{seconds: 0.9, want: "0:00:00"},
		{seconds: 59.999, want: "0:00:59"},
		{seconds: 60, want: "0:01:00"},
		{seconds: 3599, want: "0:59:59"},
		{seconds: 3600, want: "1:00:00"},
		{seconds: 5550.4, want: "1:32:30"},
		{seconds: 36000, want: "10:00:00"},
		{seconds: -1, want: "0:00:00"},
	}

	for _, each := range cases {
		t.Run(each.want, func(t *testing.T) {
			mustMatch(t, formatPosition(each.seconds), each.want)
		})
	}
}

func TestApplyOnePropertyChange(t *testing.T) {
	playing := playbackState{item: 1, position: "0:00:10", duration: "0:30:00"}

	cases := []struct {
		name   string
		start  playbackState
		change propertyChange
		want   playbackState
		atOnce bool
	}{
		{
			name:   "a pause the operator must see at once",
			start:  playing,
			change: changeOf("pause", "true"),
			want:   playbackState{paused: true, item: 1, position: "0:00:10", duration: "0:30:00"},
			atOnce: true,
		},
		{
			name:   "the first pause event repeats what is already true",
			start:  playing,
			change: changeOf("pause", "false"),
			want:   playing,
		},
		{
			name:   "the first item of a playlist counts from one",
			change: changeOf("playlist-pos", "0"),
			want:   playbackState{item: 1},
			atOnce: true,
		},
		{
			name:   "the next item",
			start:  playing,
			change: changeOf("playlist-pos", "2"),
			want:   playbackState{item: 3, position: "0:00:10", duration: "0:30:00"},
			atOnce: true,
		},
		{
			name:   "no item is loaded",
			start:  playing,
			change: changeOf("playlist-pos", "-1"),
			want:   playing,
		},
		{
			name:   "the position advances",
			start:  playing,
			change: changeOf("time-pos", "65.9"),
			want:   playbackState{item: 1, position: "0:01:05", duration: "0:30:00"},
		},
		{
			name:   "mpv has no position yet",
			start:  playbackState{item: 1},
			change: changeOf("time-pos", "null"),
			want:   playbackState{item: 1},
		},
		{
			name:   "the item's length arrives with its header",
			start:  playbackState{item: 1},
			change: changeOf("duration", "3600"),
			want:   playbackState{item: 1, duration: "1:00:00"},
		},
		{
			name:   "a stream of unknown length",
			start:  playbackState{item: 1},
			change: changeOf("duration", "null"),
			want:   playbackState{item: 1},
		},
		{
			name:   "a property the supervisor does not fold",
			start:  playing,
			change: changeOf("volume", "70"),
			want:   playing,
		},
		{
			name:   "a datum of the wrong type",
			start:  playing,
			change: changeOf("pause", `"yes"`),
			want:   playing,
		},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			state := each.start
			atOnce := state.apply(each.change)
			mustMatch(t, state, each.want)
			mustMatch(t, atOnce, each.atOnce)
		})
	}
}

func TestIdentityFromEnvironment(t *testing.T) {
	t.Run("the operator set every variable", func(t *testing.T) {
		setPlayEnvironment(t, "http://media-operator.media.svc:8080")
		identity, err := identityFromEnvironment()
		mustSucceed(t, err)
		mustMatch(t, identity, playIdentity{
			namespace:   "media",
			name:        "friday-film",
			token:       "5f3a9c",
			operatorURL: "http://media-operator.media.svc:8080",
		})
	})

	cases := []struct {
		name    string
		missing string
	}{
		{name: "no namespace", missing: playNamespaceVariable},
		{name: "no name", missing: playNameVariable},
		{name: "no operator to report to", missing: operatorURLVariable},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			setPlayEnvironment(t, "http://media-operator.media.svc:8080")
			t.Setenv(each.missing, "")
			_, err := identityFromEnvironment()
			mustFail(t, err)
		})
	}
}

func TestReporterSendsChangesAtOnceAndThrottlesThePosition(t *testing.T) {
	t.Run("a pause and an item change do not wait", func(t *testing.T) {
		useReportInterval(t, time.Hour)
		send, sent := recordReports()

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("playlist-pos", "0"),
			changeOf("time-pos", "1.0"),
			changeOf("pause", "true"),
			changeOf("time-pos", "1.0"),
			changeOf("playlist-pos", "1"),
		)
		runReporter(t.Context(), changes, testIdentity, send)

		mustMatchAll(t, itemsAndPauses(sent()), []string{"1 playing", "1 paused", "2 paused"})
	})

	t.Run("the position waits for the interval", func(t *testing.T) {
		useReportInterval(t, 20*time.Millisecond)
		send, sent := recordReports()

		changes := make(chan propertyChange)
		go func() {
			changes <- changeOf("playlist-pos", "0")
			changes <- changeOf("time-pos", "1.0")
			time.Sleep(60 * time.Millisecond)
			changes <- changeOf("time-pos", "2.0")
			close(changes)
		}()
		runReporter(t.Context(), changes, testIdentity, send)

		mustMatchAll(t, positions(sent()), []string{"", "0:00:02"})
	})

	t.Run("nothing is reported before an item is loaded", func(t *testing.T) {
		useReportInterval(t, 0)
		send, sent := recordReports()

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("pause", "false"),
			changeOf("time-pos", "1.0"),
			changeOf("duration", "60.0"),
		)
		runReporter(t.Context(), changes, testIdentity, send)

		mustMatch(t, len(sent()), 0)
	})

	t.Run("a report the operator refuses does not stop the run", func(t *testing.T) {
		useReportInterval(t, 0)
		attempts := 0
		send := func(playReport) error {
			attempts++
			return errors.New("the operator answered 403 Forbidden")
		}

		changes := make(chan propertyChange, 8)
		go feedChanges(changes,
			changeOf("playlist-pos", "0"),
			changeOf("time-pos", "1.0"),
			changeOf("time-pos", "2.0"),
		)
		runReporter(t.Context(), changes, testIdentity, send)

		mustMatch(t, attempts, 3)
	})
}

func TestSupervisorExitsWithMPVsCode(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   int
	}{
		{name: "the last item ended", script: "#!/bin/sh\nexit 0\n", want: 0},
		{name: "mpv could not play it", script: "#!/bin/sh\nexit 7\n", want: 7},
	}

	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			useMPV(t, each.script)
			useSocket(t, filepath.Join(t.TempDir(), "absent.sock"))
			useDialBudget(t, 1, time.Millisecond)
			setPlayEnvironment(t, "http://127.0.0.1:1")

			code, err := runPlayback([]string{"/media/0/film.mkv"}, make(chan os.Signal))
			mustSucceed(t, err)
			mustMatch(t, code, each.want)
		})
	}
}

func TestSupervisorRelaysTheKubeletsSignalToMPV(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	useMPV(t, trapScript(ready))
	useSocket(t, filepath.Join(t.TempDir(), "absent.sock"))
	useDialBudget(t, 1, time.Millisecond)
	setPlayEnvironment(t, "http://127.0.0.1:1")

	signals := make(chan os.Signal, 1)
	finished := playInBackground(t, signals)

	waitForFile(t, ready)
	signals <- syscall.SIGTERM

	mustMatch(t, waitForExit(t, finished), 143)
}

func TestSupervisorReportsWhatMPVObserves(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "mpv.sock")
	listener, err := net.Listen("unix", socket)
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })

	useSocket(t, socket)
	useDialBudget(t, 40, 10*time.Millisecond)
	useReportInterval(t, 0)

	reports := make(chan playReport, 16)
	operator := httptest.NewServer(reportEndpoint(reports))
	t.Cleanup(operator.Close)
	setPlayEnvironment(t, operator.URL)

	ready := filepath.Join(t.TempDir(), "ready")
	useMPV(t, trapScript(ready))

	commands := make(chan string, len(observedProperties))
	go serveMPVSocket(t, listener, commands, []string{
		`{"event":"property-change","id":1,"name":"pause","data":false}`,
		`{"event":"file-loaded"}`,
		`{"event":"property-change","id":2,"name":"playlist-pos","data":0}`,
		`{"event":"property-change","id":3,"name":"time-pos","data":5.4}`,
		`{"event":"property-change","id":4,"name":"duration","data":100.2}`,
	})

	signals := make(chan os.Signal, 1)
	finished := playInBackground(t, signals)

	mustMatchAll(t, nextCommands(t, commands), []string{
		`{"command":["observe_property",1,"pause"]}`,
		`{"command":["observe_property",2,"playlist-pos"]}`,
		`{"command":["observe_property",3,"time-pos"]}`,
		`{"command":["observe_property",4,"duration"]}`,
	})

	mustMatch(t, nextReport(t, reports), playReport{
		Namespace: "media", Name: "friday-film", Token: "5f3a9c",
		Item: 1,
	})
	mustMatch(t, nextReport(t, reports), playReport{
		Namespace: "media", Name: "friday-film", Token: "5f3a9c",
		Item: 1, Position: "0:00:05",
	})
	mustMatch(t, nextReport(t, reports), playReport{
		Namespace: "media", Name: "friday-film", Token: "5f3a9c",
		Item: 1, Position: "0:00:05", Duration: "0:01:40",
	})

	waitForFile(t, ready)
	signals <- syscall.SIGTERM
	mustMatch(t, waitForExit(t, finished), 143)
}

// testIdentity is the one run every test in this file supervises.
var testIdentity = playIdentity{
	namespace:   "media",
	name:        "friday-film",
	token:       "5f3a9c",
	operatorURL: "http://media-operator.media.svc:8080",
}

// setPlayEnvironment is the environment the operator mints into the
// playback pod, pointed at whatever report server the test runs.
func setPlayEnvironment(t *testing.T, operatorURL string) {
	t.Helper()
	t.Setenv(playNamespaceVariable, testIdentity.namespace)
	t.Setenv(playNameVariable, testIdentity.name)
	t.Setenv(playTokenVariable, testIdentity.token)
	t.Setenv(operatorURLVariable, operatorURL)
}

// trapScript is the fake mpv: it announces itself by touching the
// ready file, then waits to be ended, exiting 143 on SIGTERM the way
// a signaled process reads to the kubelet. The loop sleeps in short
// steps because a shell runs a trap only when the command in front
// of it returns, and one long sleep would swallow the signal for its
// whole length.
func trapScript(ready string) string {
	return fmt.Sprintf("#!/bin/sh\ntrap 'exit 143' TERM\n: > %s\ni=0\nwhile [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done\n", ready)
}

func useMPV(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-mpv")
	mustSucceed(t, os.WriteFile(path, []byte(script), 0o755))
	was := mpvBinary
	t.Cleanup(func() { mpvBinary = was })
	mpvBinary = path
}

func useSocket(t *testing.T, path string) {
	t.Helper()
	was := mpvSocketPath
	t.Cleanup(func() { mpvSocketPath = was })
	mpvSocketPath = path
}

func useReportInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	was := reportInterval
	t.Cleanup(func() { reportInterval = was })
	reportInterval = interval
}

// playInBackground drives runPlayback, the layer beneath supervise,
// because the entry point ends the whole process and a test cannot
// survive its own os.Exit.
func playInBackground(t *testing.T, signals <-chan os.Signal) <-chan childExit {
	t.Helper()
	finished := make(chan childExit, 1)
	go func() {
		code, err := runPlayback([]string{"/media/0/film.mkv"}, signals)
		finished <- childExit{code: code, err: err}
	}()
	return finished
}

func waitForExit(t *testing.T, finished <-chan childExit) int {
	t.Helper()
	select {
	case exit := <-finished:
		mustSucceed(t, exit.err)
		return exit.code
	case <-time.After(30 * time.Second):
		t.Fatal("the supervisor never returned")
		return 0
	}
}

// serveMPVSocket is mpv's end of the IPC socket: it reads the
// observe commands the supervisor must send first, hands them to the
// test, and then plays the scripted event stream.
func serveMPVSocket(t *testing.T, listener net.Listener, commands chan<- string, events []string) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()

	scanner := bufio.NewScanner(connection)
	for range observedProperties {
		scanner.Scan()
		commands <- scanner.Text()
	}
	for _, event := range events {
		fmt.Fprintln(connection, event)
	}
	<-t.Context().Done()
}

func reportEndpoint(reports chan<- playReport) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /report", func(w http.ResponseWriter, r *http.Request) {
		var report playReport
		json.NewDecoder(r.Body).Decode(&report)
		reports <- report
	})
	return mux
}

func nextCommands(t *testing.T, commands <-chan string) []string {
	t.Helper()
	var sent []string
	for range observedProperties {
		select {
		case command := <-commands:
			sent = append(sent, command)
		case <-time.After(30 * time.Second):
			t.Fatal("the supervisor never observed every property")
		}
	}
	return sent
}

func nextReport(t *testing.T, reports <-chan playReport) playReport {
	t.Helper()
	select {
	case report := <-reports:
		return report
	case <-time.After(30 * time.Second):
		t.Fatal("no report reached the operator")
		return playReport{}
	}
}

// waitForFile waits for the fake mpv's ready file, which the script
// touches once its signal handler is in place. A signal sent before
// that kills the shell outright, and the exit code under test never
// happens.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := os.Stat(path)
		if err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func recordReports() (reportSender, func() []playReport) {
	var reports []playReport
	send := func(report playReport) error {
		reports = append(reports, report)
		return nil
	}
	return send, func() []playReport { return reports }
}

// itemsAndPauses renders each report to the two fields the
// throttling tests are about, which reports were sent, not every
// field in them.
func itemsAndPauses(reports []playReport) []string {
	rendered := make([]string, 0, len(reports))
	for _, report := range reports {
		rendered = append(rendered, fmt.Sprintf("%d %s", report.Item, pausedOrPlaying(report.Paused)))
	}
	return rendered
}

func pausedOrPlaying(paused bool) string {
	if paused {
		return "paused"
	}
	return "playing"
}

func positions(reports []playReport) []string {
	rendered := make([]string, 0, len(reports))
	for _, report := range reports {
		rendered = append(rendered, report.Position)
	}
	return rendered
}

func feedChanges(changes chan<- propertyChange, list ...propertyChange) {
	for _, each := range list {
		changes <- each
	}
	close(changes)
}

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wanted no error, got %v", err)
	}
}

func mustFail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("wanted an error, got none")
	}
}

func mustMatch[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func mustMatchAll[T comparable](t *testing.T, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
