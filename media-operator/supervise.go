package main

// The supervisor is the playback pod's PID 1. It starts mpv against
// the sockets the claims delivered, relays the kubelet's signals to
// it, reports what mpv does to the operator, and ends with mpv's own
// exit code, so the pod's outcome is the player's outcome.
//
// The supervisor holds no API credentials and never writes
// Play.status itself. This process decodes media from the network,
// which makes it the least trusted process in the system, so what it
// can say to the control plane is one plain HTTP report, and the
// operator decides what any report means. A report that fails is
// logged and forgotten: the operator's view of the run goes stale,
// and the film keeps playing.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// mpvBinary is a variable rather than a constant because the pod
// runs the mpv its image carries, and a test runs a stand-in that
// needs no display and no sound card.
var mpvBinary = "mpv"

// reportInterval is the ceiling on how often the supervisor reports
// while the position advances. mpv sends a time-pos event several
// times a second, and each report becomes one status write in the
// operator, so an unthrottled run would rewrite the Play as fast as
// the player counts.
var reportInterval = 5 * time.Second

// reportTimeout bounds one report. The reporting loop is single
// file, so a POST that hung without a deadline would stall every
// report behind it.
var reportTimeout = 5 * time.Second

// displayAppIDVariable arrives from the display claim's CDI spec,
// not from the operator. The display operator writes it into the
// container's environment at run time, after the pod spec is fixed,
// so the operator could not know the value to set.
const displayAppIDVariable = "DISPLAY_APP_ID"

// playIdentity is what the supervisor must know before it starts
// mpv. The operator mints every field into the pod's environment;
// the token is the one that proves the reports come from this pod.
type playIdentity struct {
	namespace   string
	name        string
	token       string
	operatorURL string
}

// identityFromEnvironment refuses to start a run nobody can see: a
// pod with no play name or no operator address would play into a
// status that never updates. The token is the one field that may be
// empty, because its loss is smaller: the operator refuses the
// reports, and the pod plays on unwatched.
func identityFromEnvironment() (playIdentity, error) {
	identity := playIdentity{
		namespace:   os.Getenv(playNamespaceVariable),
		name:        os.Getenv(playNameVariable),
		token:       os.Getenv(playTokenVariable),
		operatorURL: os.Getenv(operatorURLVariable),
	}
	required := []struct {
		variable string
		value    string
	}{
		{playNamespaceVariable, identity.namespace},
		{playNameVariable, identity.name},
		{operatorURLVariable, identity.operatorURL},
	}
	for _, each := range required {
		if each.value == "" {
			return playIdentity{}, fmt.Errorf("%s is unset; the operator sets it on the playback pod", each.variable)
		}
	}
	return identity, nil
}

// mpvArguments is the whole of how mpv is told to play. The gpu
// video output with the Wayland EGL context, because the display
// claim delivers a compositor socket and Debian's mpv segfaults in
// --vo=dmabuf-wayland. VAAPI, because the render claim delivers the
// node that decodes. The PipeWire audio output, because Wayland
// carries no audio and the sink claim delivers that socket. The IPC
// server, because the socket is the supervisor's only way in. The
// window's app-id routes it to the allocated output; mpv has no
// environment mechanism for options, so the supervisor adds the flag
// itself when the display claim delivered an id.
//
// The list ends with -- because a media path that starts with a dash
// would otherwise read as a flag.
//
// --no-input-terminal is deliberately absent. mpv installs its
// SIGTERM handler only on the terminal-input path, and without the
// handler a relayed SIGTERM ends the player the hard way instead of
// letting it quit.
func mpvArguments(items []string) []string {
	arguments := []string{
		"--vo=gpu",
		"--gpu-context=wayland",
		"--hwdec=vaapi",
		"--fullscreen",
		"--ao=pipewire",
		"--input-ipc-server=" + mpvSocketPath,
	}
	if applicationID := os.Getenv(displayAppIDVariable); applicationID != "" {
		arguments = append(arguments, "--wayland-app-id="+applicationID)
	}
	// The declared start applies to the first file mpv loads and to
	// no later playlist entry, which is exactly what spec.start
	// means: the run begins here, and later items begin at their
	// own start.
	if start := os.Getenv(playStartVariable); start != "" {
		arguments = append(arguments, "--start="+start)
	}
	arguments = append(arguments, "--")
	return append(arguments, items...)
}

// supervise is the mode the player image's entrypoint selects. It
// ends the process itself because mpv's exit code decides whether
// the pod succeeds, and a pod that succeeds is a Play that finished.
func supervise(items []string) {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	code, err := runPlayback(items, signals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervise: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// runPlayback is the shape of the run: start mpv, watch its socket,
// wait for its exit. Reporting failures never reach this function's
// error, because they are not reasons to stop playing.
func runPlayback(items []string, signals <-chan os.Signal) (int, error) {
	identity, err := identityFromEnvironment()
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, errors.New("supervise needs at least one item to play")
	}

	player := exec.Command(mpvBinary, mpvArguments(items)...)
	// mpv's own output goes straight to the pod's log. The
	// supervisor adds nothing to it, and a relay through this
	// process would only be one more place for it to stall.
	player.Stdout = os.Stdout
	player.Stderr = os.Stderr
	if err := player.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", mpvBinary, err)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	exits := reapChildren(player.Process.Pid)
	go relaySignals(ctx, signals, player.Process)

	reporting := make(chan struct{})
	go func() {
		defer close(reporting)
		reportPlayback(ctx, identity)
	}()

	exit := <-exits
	// The run ends only once the reporting side has stopped. mpv's
	// socket is gone the moment mpv is, and a report written after
	// the exit code is read would describe a player that no longer
	// exists.
	stop()
	<-reporting

	if exit.err != nil {
		return 0, exit.err
	}
	return exit.code, nil
}

// childExit is what a child's end looks like to the supervisor: an
// exit code, or the failure that kept the wait from reading one.
type childExit struct {
	code int
	err  error
}

// reapChildren waits for every child, not only for mpv. A process
// whose parent dies is reparented to PID 1, and a child nobody waits
// for stays a zombie until the pod ends.
//
// This loop exists instead of exec.Cmd.Wait because two waiters race
// for one exit status: whichever reaches the kernel first is the
// only one that gets it, and a reaper that consumed mpv's status
// would leave Wait with nothing and the pod's phase wrong. One
// waiter that reaps everything and knows mpv's pid cannot lose it.
func reapChildren(target int) <-chan childExit {
	exits := make(chan childExit, 1)
	go func() {
		for {
			var status syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &status, 0, nil)
			// EINTR is ordinary here: the Go runtime signals its own
			// threads to preempt them, and the wait just goes again.
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				exits <- childExit{err: fmt.Errorf("wait for pid %d: %w", target, err)}
				return
			}
			if pid != target {
				continue
			}
			exits <- childExit{code: exitCode(status)}
			return
		}
	}()
	return exits
}

// exitCode repeats the shell's convention for a process a signal
// killed: 128 plus the signal number. The kubelet reads the
// container's exit code the same way, and a nonzero code is what
// makes the pod fail.
func exitCode(status syscall.WaitStatus) int {
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return status.ExitStatus()
}

// relaySignals exists because the kernel runs no default action for
// a signal sent to PID 1. The SIGTERM the kubelet sends at the start
// of a pod's grace period reaches mpv only if this process passes it
// on.
func relaySignals(ctx context.Context, signals <-chan os.Signal, player *os.Process) {
	for {
		select {
		case <-ctx.Done():
			return
		case received := <-signals:
			// A failure to signal means mpv has already exited, and
			// the wait is about to say so.
			_ = player.Signal(received)
		}
	}
}

// reportPlayback is the reporting side of the run, and it gives up
// quietly. A supervisor that cannot reach mpv's socket still has a
// film to supervise; the loss is the operator's view of it, and the
// stderr line is the record of that loss.
func reportPlayback(ctx context.Context, identity playIdentity) {
	connection, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "supervise: no reports for this run: %v\n", err)
		return
	}
	defer connection.Close()

	// The connection closes on the context rather than on a
	// deadline. The read blocks until mpv writes, which can be
	// minutes apart in a paused film, and closing the socket is what
	// ends the read when mpv is gone.
	defer context.AfterFunc(ctx, func() { connection.Close() })()

	if err := observeProperties(connection, observedProperties); err != nil {
		fmt.Fprintf(os.Stderr, "supervise: no reports for this run: %v\n", err)
		return
	}

	changes := make(chan propertyChange, 16)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		defer close(changes)
		if err := readEvents(ctx, connection, changes); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "supervise: mpv's socket: %v\n", err)
		}
	}()

	runReporter(ctx, changes, identity, httpReportSender(identity.operatorURL))
	<-reading
}

// playbackState is what the supervisor knows about the run at one
// moment. The fields are the report's fields, held between events,
// because each event carries one property and a report carries all
// of them.
type playbackState struct {
	paused   bool
	item     int
	position string
	duration string
}

// reportable holds reports back until mpv has said which item plays.
// mpv's playlist-pos counts from zero and reads -1 before anything
// loads; the API counts from one, the way a person counts tracks, so
// an item below one describes no playback at all.
func (s playbackState) reportable() bool {
	return s.item >= 1
}

func (s playbackState) report(identity playIdentity) playReport {
	return playReport{
		Namespace: identity.namespace,
		Name:      identity.name,
		Token:     identity.token,
		Paused:    s.paused,
		Item:      s.item,
		Position:  s.position,
		Duration:  s.duration,
	}
}

// apply folds one property change into the state, and its return
// value says whether the change earns a report at once. A pause and
// an item change are the two things a person watching kubectl is
// waiting to see, and both are rare. A position that advances is
// neither rare nor surprising, so it waits for the interval.
func (s *playbackState) apply(change propertyChange) bool {
	if !change.known() {
		return false
	}
	switch change.Name {
	case "pause":
		var paused bool
		if err := json.Unmarshal(change.Data, &paused); err != nil {
			return false
		}
		changed := paused != s.paused
		s.paused = paused
		return changed
	case "playlist-pos":
		var position int
		if err := json.Unmarshal(change.Data, &position); err != nil || position < 0 {
			return false
		}
		item := position + 1
		changed := item != s.item
		s.item = item
		return changed
	case "time-pos":
		var seconds float64
		if err := json.Unmarshal(change.Data, &seconds); err != nil {
			return false
		}
		s.position = formatPosition(seconds)
		return false
	case "duration":
		var seconds float64
		if err := json.Unmarshal(change.Data, &seconds); err != nil {
			return false
		}
		s.duration = formatPosition(seconds)
		return false
	}
	return false
}

// formatPosition writes seconds as H:MM:SS, because the value's one
// job is to be read in kubectl get output. The seconds are floored,
// not rounded: a position that reads 0:00:01 while the first second
// still plays is wrong in the direction a person notices.
func formatPosition(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	whole := int(math.Floor(seconds))
	return fmt.Sprintf("%d:%02d:%02d", whole/3600, whole%3600/60, whole%60)
}

// reportSender is how a report reaches the operator. The loop takes
// a function rather than an address so a test can catch the reports
// with no HTTP server at all.
type reportSender func(playReport) error

// runReporter is the whole reporting rule in one loop: fold the
// change, send it now when it is one of the two that matter, and
// otherwise send no more than one report per interval.
func runReporter(ctx context.Context, changes <-chan propertyChange, identity playIdentity, send reportSender) {
	var state playbackState
	var sent time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case change, open := <-changes:
			if !open {
				return
			}
			atOnce := state.apply(change)
			if !state.reportable() {
				continue
			}
			if !atOnce && time.Since(sent) < reportInterval {
				continue
			}
			sent = time.Now()
			if err := send(state.report(identity)); err != nil {
				fmt.Fprintf(os.Stderr, "supervise: report: %v\n", err)
			}
		}
	}
}

// httpReportSender POSTs the report with no credentials. The token
// in the body is what a credential would be: the operator minted it
// into this pod alone, so a report that carries it came from here.
func httpReportSender(operatorURL string) reportSender {
	client := &http.Client{Timeout: reportTimeout}
	endpoint := strings.TrimSuffix(operatorURL, "/") + "/report"
	return func(report playReport) error {
		body, err := json.Marshal(report)
		if err != nil {
			return err
		}
		response, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		defer response.Body.Close()
		// The body is drained even though it is empty, because the
		// connection returns to the pool only when the read reaches
		// EOF.
		_, _ = io.Copy(io.Discard, response.Body)
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("the operator answered %s", response.Status)
		}
		return nil
	}
}
