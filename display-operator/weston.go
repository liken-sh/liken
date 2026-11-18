package main

// This file runs weston, the Wayland compositor, in the operator's pod.
// It holds two container roles: declare writes weston.ini, and the
// compositor role runs weston as a child process.
//
// The operator's exclusive claim gives the pod the graphics card. Only
// the DRM master of a card can set its modes, and weston becomes master
// because it opens the card first. The operator container opens the
// same card to read its modes, and the card gate (cardgate.go) keeps
// those opens from taking master from weston.
//
// weston runs ivi-shell, which shows a surface only after a controller
// gives it a rectangle and a stacking position. The controller is the
// operator's module liken-layout.so, and layoutlink.go is the
// operator's end of the module's control socket. The module opens one
// Wayland socket for each prepared claim, so the socket that a surface
// connects on identifies its claim. A client can set its app-id to any
// string, so nothing routes by app-id.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// hotplugShim is the preload library that westonEnvironment sets for
// weston. The weston base image builds it from
// weston/hotplug/udev-kernel-group.c at the top of the repository.
const hotplugShim = "/usr/lib/liken/udev-kernel-group.so"

// westonBinary is weston's path in the image. compositorProcesses finds
// weston by comparing each process's exe link with this path.
const westonBinary = "/usr/bin/weston"

// configWaitTimeout is how long the compositor role waits for the
// config that declare writes. When it times out, the container exits
// with an error and the kubelet restarts it.
const configWaitTimeout = 30 * time.Second

// fileWaitFallback is the longest that waitForFile waits between two
// checks for the file. An inotify event for the file ends a wait
// sooner, but no event can come while the file's directory does not
// exist.
const fileWaitFallback = time.Second

// socketDialTimeout is how long the probe waits for the compositor to
// accept a connection.
const socketDialTimeout = 500 * time.Millisecond

// socketWatchInterval is how often watchSocket probes the compositor's
// socket, which also sets how soon after compositorHungLimit it kills a
// hung compositor.
const socketWatchInterval = 1 * time.Second

// declareMode and compositorMode are the arguments that select a
// container's role. Every container of the pod runs the same image, and
// a container with no argument runs the operator.
const (
	declareMode    = "declare"
	compositorMode = "weston"
)

// westonConfig builds weston.ini for the card's outputs. modes maps a
// connector to the mode that a claim or the monitor's Display set,
// written as name or name@refresh, because weston's mode= key accepts
// both. Every other connector gets mode=preferred.
//
// Every connector gets an [output] section, with or without a monitor.
// weston reads the file only at startup, so the section must already
// exist when a monitor is plugged in later.
func westonConfig(outputs []Output, modes map[string]string) string {
	var config strings.Builder
	config.WriteString(`# Written by display.liken.sh at startup. Every edit is lost on the
# next restart of the operator.

[core]
# ivi-shell shows a surface where a controller puts it, and nowhere
# else. The controller is the module on the next line, and the
# operator is the one program that tells it what to place.
shell=ivi-shell.so

# liken-layout.so is this operator's controller. weston 16 has no
# ivi-module= key: a controller is an ordinary module in this list,
# loaded after the shell, and it finds the shell through
# ivi_layout_get_api.
modules=liken-layout.so

# The GL renderer advertises zwp_linux_dmabuf_v1 at version 4. mpv
# refuses to bind the protocol below version 4, and the pixman
# renderer publishes no dmabuf feedback, so it advertises version 3
# and mpv falls back to software paths.
renderer=gl

# A machine with monitors on it has no keyboard and no mouse. Weston
# otherwise refuses to start when it finds no input device.
require-input=false

# none lets weston start with no output at all. The default, any,
# ends weston when no connector on the card has a monitor, and this
# machine is expected to boot with its panel showing another input or
# with the monitor switched off. A compositor that starts anyway holds
# the socket every claim is delivered on, so a pod keeps running and
# its picture arrives with the monitor.
require-outputs=none

# 0 turns the idle timeout off. Under desktop-shell the 300-second
# default fades and sleeps the screens, and with no input device
# nothing ever wakes them. The 0 keeps every screen lit whichever
# shell weston loads.
idle-time=0
`)
	for _, output := range outputs {
		mode := preferredMode
		if set := modes[output.Connector]; set != "" {
			mode = set
		}
		fmt.Fprintf(&config, `
[output]
name=%s
mode=%s
`, output.Connector, mode)
		if scale := outputScale(output, mode); scale > 1 {
			fmt.Fprintf(&config, "scale=%d\n", scale)
		}
	}
	return config.String()
}

// scaledWidth is the narrowest mode width that gets an output scale of
// 2, so a 4K panel lays out at the 1080p size that clients draw for. A
// single threshold lets a person predict the scale from the mode.
const scaledWidth = 3840

// outputScale returns 2 when the mode that the [output] section writes
// is at least scaledWidth wide, and 1 otherwise. For mode=preferred it
// reads the width of the output's first mode, and a connector with no
// monitor has no modes and gets 1.
//
// weston sends the scale to each client on the output. A client that
// lays out in logical pixels, such as the media browser, draws at the
// 1080p size and renders at the panel's resolution. weston scales up
// the picture of a client that ignores the scale.
func outputScale(output Output, mode string) int {
	if mode == preferredMode {
		if len(output.Modes) == 0 {
			return 1
		}
		mode = output.Modes[0]
	}
	if modeWidth(mode) >= scaledWidth {
		return 2
	}
	return 1
}

// modeWidth returns the width in a mode name of the form 3840x2160 or
// 3840x2160@60, and 0 for any other name.
func modeWidth(mode string) int {
	width, _, found := strings.Cut(mode, "x")
	if !found {
		return 0
	}
	n, err := strconv.Atoi(width)
	if err != nil {
		return 0
	}
	return n
}

// writeWestonConfig writes the config to path, in the pod's own volume,
// because the file describes the monitors that this pod found. declare
// writes it at startup, and the operator container writes it again when
// a claim sets a mode. Each builds the whole file from a walk of the
// connectors and the mode record, so nothing reads the file back.
func writeWestonConfig(path string, outputs []Output, modes map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(westonConfig(outputs, modes)), 0o644)
}

// declaredDisplays reads the Displays that supply the spec.mode values to
// declare. It is a variable so a test can run declare without an API
// server.
var declaredDisplays = declaredDisplaysFromAPI

// declare writes the mode record and weston's config. It runs as an
// init container, so the kubelet starts the compositor's container only
// after the config is on disk.
func declare() {
	card := claimedCard()

	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		fatal("%s registers no connectors under %s/class/drm", card, sysRoot)
	}
	live := connected(outputs)
	if len(live) == 0 {
		// weston starts with no output (require-outputs=none), so this
		// is not a failure.
		fmt.Fprintf(os.Stderr, "%s has no connected monitor on its %d connectors\n", card, len(outputs))
	}
	for _, output := range live {
		monitor := monitorID(output.Monitor)
		if monitor == "" {
			monitor = "a monitor with no readable EDID"
		}
		fmt.Printf("%s: %s has %s\n", DriverName, output.Connector, monitor)
	}
	// The record has the modes that claims set. It is empty in a new
	// pod, because it is in the pod's own volume, and it keeps its
	// entries when the kubelet restarts the pod's containers.
	// addDisplayModes adds the spec.mode of each connected monitor's
	// Display for a connector with no entry.
	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		fatal("%v", err)
	}
	record = addDisplayModes(record, live, declaredDisplays())
	if err := writeModeRecord(modeRecordPath, record); err != nil {
		fatal("writing %s: %v", modeRecordPath, err)
	}
	if err := writeWestonConfig(westonConfigPath, outputs, record); err != nil {
		fatal("writing %s: %v", westonConfigPath, err)
	}
}

// compositorProcesses returns the sorted pids of the processes under
// procRoot whose exe link is westonBinary. The pod shares one process
// namespace, so the operator container sees weston's process.
func compositorProcesses(procRoot string) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		// /proc also has entries that are not processes, such as self.
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// The link reads as weston's path even though this container
		// has no file there. Readlink fails for a process that exited
		// after the listing.
		binary, err := os.Readlink(filepath.Join(procRoot, entry.Name(), "exe"))
		if err != nil || binary != westonBinary {
			continue
		}
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	return pids
}

// compose runs weston as a child process, and runs it again after each
// exit that the operator requested (restartrequests.go). Any other exit of
// weston ends this container, and the kubelet restarts it.
func compose() {
	card := claimedCard()
	socketDir := envOr("SOCKET_DIR", defaultSocketDir)

	if err := waitForFile(context.Background(), westonConfigPath, configWaitTimeout); err != nil {
		fatal("%v", err)
	}

	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		fatal("making %s: %v", socketDir, err)
	}

	requests := restartRequests(restartRequestsPath)
	if err := requests.clear(); err != nil {
		fatal("clearing the restart request files in %s: %v", restartRequestsPath, err)
	}

	// supervise passes the kubelet's SIGTERM on to weston, so weston
	// releases the card before the grace period ends.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	// libwayland creates its socket with the process umask and does not
	// chmod it. connect() needs write permission, so under a umask of
	// 022 a client with another uid is refused. weston inherits this
	// umask, and this process creates no other files after this point.
	unix.Umask(0)
	fmt.Printf("%s: starting the compositor on %s\n", DriverName, card)
	argv := westonArgv(card, westonConfigPath, socketName, os.Getenv(westonLogScopesVariable))
	environment := westonEnvironment(os.Environ(), socketDir)
	status, err := supervise(func() (*exec.Cmd, error) {
		compositor := &exec.Cmd{Path: westonBinary, Args: argv, Env: environment, Stdout: os.Stdout, Stderr: os.Stderr}
		return compositor, compositor.Start()
	}, requests, stop)
	if err != nil {
		fatal("running %s: %v", westonBinary, err)
	}
	os.Exit(status)
}

// westonArgv builds weston's command line. card is a name such as
// card1, not a path, because weston's --drm-device takes a name. The
// card comes from the claim, so the manifest cannot set this flag.
// logScopes is the value of westonLogScopesVariable.
func westonArgv(card, configPath, socketName, logScopes string) []string {
	argv := []string{
		westonBinary,
		"--backend=drm",
		"--drm-device=" + card,
		"--config=" + configPath,
		"--socket=" + socketName,
	}
	if scopes := loggerScopes(logScopes); scopes != "" {
		argv = append(argv, "--logger-scopes="+scopes)
	}
	return argv
}

// westonLogScopesVariable is the environment variable that lists, comma
// separated, the weston debug scopes to write to weston's standard
// error, which is this container's log. It is empty by default. The
// drm-backend scope logs, on each repaint, which display plane weston
// tries for each view and why it rejects it. Nothing else in the pod
// shows why a view goes to the renderer and not to a plane.
//
// weston reads scopes only from its command line, so this is an
// environment variable and not a weston.ini key, and a change takes
// effect when the compositor starts again. The operator does not pass
// --debug, because --debug advertises weston_debug_v1 on every socket.
// A client on any claim's socket could then subscribe to the proto
// scope and read the protocol traffic of the other claims, and could
// capture every output. The container log is readable only by someone
// who can read pod logs in the operator's namespace.
//
// weston's in-memory flight recorder subscribes only to the log scope,
// so weston formats no drm-backend line until this variable names the
// scope. Then each repaint writes many lines, and a playing film
// repaints on every frame. The DaemonSet sets the variable on every
// machine at once, so clear it when you are done.
const westonLogScopesVariable = "WESTON_LOG_SCOPES"

// loggerScopes builds the value of weston's --logger-scopes flag, or
// returns "" when requested names no scope other than log. The flag
// replaces weston's default subscription to the log scope, so the list
// starts with log to keep the messages of weston and the layout module
// in the container log.
func loggerScopes(requested string) string {
	scopes := []string{"log"}
	for _, scope := range strings.Split(requested, ",") {
		scope = strings.TrimSpace(scope)
		if scope != "" && !slices.Contains(scopes, scope) {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 1 {
		return ""
	}
	return strings.Join(scopes, ",")
}

// westonEnvironment returns the container's environment with the
// settings that only weston needs.
func westonEnvironment(environ []string, socketDir string) []string {
	return append(append([]string{}, environ...),
		// weston opens devices only through libseat, and the noop
		// backend needs no seatd, no logind, and no VT. It opens the
		// card with a plain open(), and the kernel makes the first
		// process that opens a card its DRM master with no capability
		// check, so weston runs with no root and no capability. libseat
		// never selects noop by itself.
		"LIBSEAT_BACKEND=noop",
		// weston receives hotplug events on the netlink group that only
		// udevd sends on, and `liken` runs no udevd. The shim moves the
		// subscription to the kernel's netlink group, which carries the
		// same events. The operator's binary is static and cannot load
		// it.
		"LD_PRELOAD="+hotplugShim,
		// weston creates its socket in XDG_RUNTIME_DIR, which client
		// containers mount.
		"XDG_RUNTIME_DIR="+socketDir,
	)
}

// waitForFile returns when the file exists, and fails when ctx ends or
// the timeout passes. It checks again on each inotify event for the
// file (arrivals.go), and at least every fileWaitFallback.
func waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	watch := newArrivals(path)
	defer watch.close()
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		watch.ready()
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		watch.wait(bounded, fileWaitFallback)
		if err := ctx.Err(); err != nil {
			return err
		}
		if bounded.Err() != nil {
			return fmt.Errorf("%s did not appear within %s", path, timeout)
		}
	}
}

// compositorReplyTimeout is how long the probe waits for the reply to
// wl_display.sync. A running event loop replies at once, so the
// timeout leaves room for a loaded machine.
const compositorReplyTimeout = 2 * time.Second

// compositorLiveness is the result of one probe. reason is the reason
// of the CompositorServing condition, and detail is the socket's error
// text when the probe failed.
type compositorLiveness struct {
	serving bool
	reason  string
	detail  string
}

// compositorServing reports whether a compositor replies on the socket.
// The pass that publishes the ResourceSlice calls it to decide the
// NoExecute taint.
func compositorServing(socketPath string) bool {
	return probeCompositor(socketPath).serving
}

// probeCompositor connects to the socket, sends wl_display.sync, and
// waits for any event. A connect alone proves nothing, because the
// kernel accepts connections on a listening socket even when the
// process behind it is stopped.
//
// The result is CompositorDownReason when the socket refuses the
// connection or closes it during the exchange, because weston has
// exited and its container restarts. It is CompositorHungReason when
// the socket accepts and nothing replies, because the process still
// runs and only a kill ends it.
func probeCompositor(socketPath string) compositorLiveness {
	socket, err := net.DialTimeout("unix", socketPath, socketDialTimeout)
	if err != nil {
		return compositorLiveness{reason: CompositorDownReason, detail: err.Error()}
	}
	defer func() { _ = socket.Close() }()
	return probeConnection(socket)
}

// probeConnection runs the probe's exchange on an open connection. It
// is separate from the dial because a deadline on a unix socket uses
// the real clock, so a test runs it on an in-memory connection, whose
// deadline uses the fake clock of synctest.
func probeConnection(socket net.Conn) compositorLiveness {
	if err := socket.SetDeadline(time.Now().Add(compositorReplyTimeout)); err != nil {
		return compositorLiveness{reason: CompositorDownReason, detail: err.Error()}
	}

	client := newWaylandClient(socket)
	var words waylandWords
	words.putUint(client.newID())
	if err := client.request(displayObject, displaySync, words); err != nil {
		return failedProbe(err)
	}
	if _, err := client.event(); err != nil {
		return failedProbe(err)
	}
	return compositorLiveness{serving: true, reason: CompositorServingReason}
}

// failedProbe returns CompositorHungReason for an expired deadline, and
// CompositorDownReason for any other error, which means the socket
// closed during the exchange.
func failedProbe(err error) compositorLiveness {
	reason := CompositorDownReason
	if errors.Is(err, os.ErrDeadlineExceeded) {
		reason = CompositorHungReason
	}
	return compositorLiveness{reason: reason, detail: err.Error()}
}

// compositorHungLimit is how long the probe must report Hung before
// the operator sends SIGKILL to the compositor. A compositor under load
// replies late, so a shorter hang is not killed. The kubelet restarts
// only a process that exits, so nothing else ends a frozen weston.
const compositorHungLimit = 10 * time.Second

// hungCompositor tracks one outage for the socket watch: when the probe
// first reported Hung, and whether the operator has killed the
// compositor since then.
type hungCompositor struct {
	since  time.Time
	killed bool
}

// due reports whether the probe has reported Hung for at least
// compositorHungLimit with no kill in this outage. Any result other
// than Hung ends the outage.
func (h *hungCompositor) due(live compositorLiveness, now time.Time) bool {
	if live.reason != CompositorHungReason {
		h.since, h.killed = time.Time{}, false
		return false
	}
	if h.since.IsZero() {
		h.since = now
	}
	return !h.killed && now.Sub(h.since) >= compositorHungLimit
}

// done records the kill, so that the watch does not kill the weston
// that starts in its place while the outage lasts.
func (h *hungCompositor) done() {
	h.killed = true
}

// westonRestarts reads how many times the kubelet has restarted the
// compositor's container, from the status of the operator's own pod,
// where the compositor is a native sidecar. The container exits only
// when weston exits with no restart request file, so each restart is a crash.
// A nil westonRestarts, for an operator that a person runs outside a
// pod, always reads zero.
type westonRestarts struct {
	client    *apiclient.Client
	namespace string
	pod       string
	// pods is the store of this node's pods. growth reads the pod from
	// it, and calls the API server only when the store does not have
	// the pod (objectcache.go).
	pods clusterStores
	// seen is the count from the last read, and known is whether there
	// was one. The first read only sets the baseline, because after a
	// restart of the operator container alone, the count already has
	// restarts from before this process started.
	seen  int
	known bool
}

func newWestonRestarts(client *apiclient.Client, namespace, pod string) *westonRestarts {
	if client == nil || namespace == "" || pod == "" {
		return nil
	}
	return &westonRestarts{client: client, namespace: namespace, pod: pod}
}

// growth returns how many times the kubelet has restarted the
// compositor since the last read. A count that went down means the pod
// was replaced, because a restart count never falls, and growth
// returns zero.
func (r *westonRestarts) growth() (int, error) {
	if r == nil {
		return 0, nil
	}
	pod, err := r.pods.pod(r.client, r.namespace, r.pod)
	if err != nil {
		return 0, err
	}
	count := pod.Status.restarts(compositorMode)
	grew := count - r.seen
	if !r.known || grew < 0 {
		grew = 0
	}
	r.seen, r.known = count, true
	return grew, nil
}
