package main

// Running the compositor.
//
// Weston is the daemon this operator runs, in the pattern's sense: it
// holds the hardware that the operator's own claim acquired, and what
// it holds is what the operator publishes. DRM master is one per card,
// so exactly one process may set a mode on the card, and the exclusive
// display claim is what makes weston that process.
//
// ivi-shell is the shell for a screen a controller places surfaces
// on. The shell itself decides nothing: a surface stays invisible
// until a controller states a rectangle and a place in the stacking
// order for it. The controller is this operator's own module,
// liken-layout.so, which weston.ini names in [core] modules=, and
// layoutlink.go is the operator's side of the control socket the
// module listens on.
//
// The module opens one Wayland socket for each prepared claim, so the
// socket a surface arrives on names the claim that drew it. That is
// an identity nothing in a consumer's container can change, where the
// app-id is a string the client sets.
//
// This file holds two of the pod's three roles: declare, which
// writes the config, and the compositor role, which execs weston so
// that weston replaces the process, weston's exit is the container's
// exit, and the kubelet is the supervision.
//
// The flags match what the lab machine runs today, weston 14.0.2 with
// LIBSEAT_BACKEND=noop. That backend opens the device path with a
// plain open(), which is all a container can do, and the kernel hands
// DRM master to the first process to open the card with no capability
// check. So the pod needs no root, no capability, and no seat manager.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// hotplugShim is the path of the preload library in the image. The
// library moves the compositor's hotplug subscription from udevd's
// netlink group to the kernel's netlink group. The comment in
// hotplug/udev-kernel-group.c explains why.
const hotplugShim = "/usr/lib/liken/udev-kernel-group.so"

// westonBinary is weston's own path in the image, in full because
// exec resolves no PATH.
const westonBinary = "/usr/bin/weston"

// configWaitTimeout bounds the compositor role's wait for the
// config the declare container writes. A config that never arrives
// is a failure to report, and the container's restart is the
// retry.
const configWaitTimeout = 30 * time.Second

// filePollInterval is how often waitForFile checks for the file. A
// new file raises no event that a program can wait on without
// another dependency.
//
// The compositor role's wait for its config is this interval's one
// reader, and that wait ends within a tick or two, because the
// declare container exits before this one starts.
const filePollInterval = 100 * time.Millisecond

// socketDialTimeout is how long a check waits for the compositor to
// accept the connection. Accepting a client is the first thing an
// event loop does, so a compositor that cannot answer in half a
// second is not serving anybody.
const socketDialTimeout = 500 * time.Millisecond

// socketWatchInterval is how often the operator connects to the
// compositor's socket. A connect and a close once a second costs the
// compositor what any Wayland tool costs it, and the watch runs for
// the life of the pod, so it stays gentle where the config wait is
// quick.
const socketWatchInterval = 1 * time.Second

// declareMode and compositorMode are the arguments that select the
// pod's other two roles.
//
// One image, three containers: the manifest passes one of these as
// the argument, and no argument at all runs the operator.
const (
	declareMode    = "declare"
	compositorMode = "weston"
)

// westonConfig builds the weston.ini for one set of outputs.
//
// Modes is the record of what the claims on this machine asked
// for, keyed by connector. A connector with an entry gets that
// mode, name or name@refresh exactly as the claim spelled it,
// because weston's mode= line reads both forms. Every other
// connector gets the one its monitor prefers.
// The config is always built from a fresh connector walk and the
// record, and never parsed back, so this function is the only thing
// that knows the file's shape.
//
// Every setting here is a requirement of a compositor in a pod on a
// machine with no keyboard. No setting is a deployment's preference,
// so the operator writes the file instead of taking one.
//
// Every connector gets a section, dark or lit. Weston parses this
// file once, at startup. It enables only the heads whose connector
// reports a monitor, so a dark section does nothing at first. When a
// monitor arrives, that section configures the new output.
func westonConfig(outputs []Output, modes map[string]string) string {
	var config strings.Builder
	config.WriteString(`# Written by display.liken.sh at startup. Every edit is lost on the
# next restart of the operator.

[core]
# ivi-shell shows a surface where a controller puts it, and nowhere
# else. The controller is the module on the next line, and the
# operator is the one program that tells it what to place.
shell=ivi-shell.so

# liken-layout.so is this operator's controller. weston 14 has no
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

# 0 turns the idle timeout off. Under desktop-shell the 300-second
# default fades and sleeps the screens, and with no input device
# nothing ever wakes them. The 0 keeps every screen lit whichever
# shell weston loads.
idle-time=0
`)
	for _, output := range outputs {
		mode := preferredMode
		if stated := modes[output.Connector]; stated != "" {
			mode = stated
		}
		// The section states the name, the mode, and the scale, and no
		// app-ids= line: ivi-shell reads no such key, because the
		// controller states which output every surface goes on.
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

// scaledWidth is the narrowest mode that gets an output scale of 2.
// A 4K panel at scale 2 lays out as a 1080p one, which is the size
// every client draws for. The rule has one threshold, so a person can
// predict it from the mode alone.
const scaledWidth = 3840

// outputScale is the integer scale an output section states for this
// mode: 2 at 4K and wider, and 1 below it. The width is read from
// the mode the section writes, so the scale always matches the mode
// weston runs: a claim's own spelling, or the monitor's preferred
// mode when the section says preferred. A dark connector has no
// modes and gets 1, like every narrower panel.
//
// Weston states the scale to every client on the output. A client
// that lays out in logical pixels, such as the media browser, draws a
// 4K panel at the 1080p size and rasters at the panel's resolution.
// A client that does not is scaled up by the compositor so that it is
// readable.
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

// modeWidth is the width a mode name states, from 3840x2160 or
// 3840x2160@60, and 0 for a name in neither form.
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

// writeWestonConfig writes the compositor's config where weston reads
// it.
//
// The file describes the monitors this pod found, so the volume is
// the pod's own.
//
// Two roles write it. The declare container writes it at
// startup from the record it finds, and the operator container writes
// it again whenever a claim states a mode. Both build the whole file
// from a connector walk and the record, so neither has to read what
// the other wrote.
func writeWestonConfig(path string, outputs []Output, modes map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(westonConfig(outputs, modes)), 0o644)
}

// declare enumerates the card's connectors, writes the compositor's
// config, and ends the process.
//
// The enumeration and the write are one init container, so the
// config is on disk before the compositor's container starts, and
// the ordering is the kubelet's, not a wait either role holds.
func declare() {
	card := claimedCard()

	// The outputs are enumerated once, here, and the config the
	// compositor reads is written from that enumeration. Every
	// connector gets an [output] section, dark or lit. The compositor
	// parses the file once, so the section for a connector must exist
	// before a monitor arrives on it.
	outputs := discoverOutputs(sysRoot, card)
	if len(outputs) == 0 {
		fatal("%s registers no connectors under %s/class/drm", card, sysRoot)
	}
	live := connected(outputs)
	if len(live) == 0 {
		// Every connector still gets a config section below, so
		// weston can light whichever connector a monitor arrives on.
		// The weston container holds its own start until one does
		// (monitorwait.go), so this line is a report and not a
		// failure.
		fmt.Fprintf(os.Stderr, "%s has no monitor on any of its %d connectors\n", card, len(outputs))
	}
	for _, output := range live {
		monitor := monitorID(output.Monitor)
		if monitor == "" {
			monitor = "a monitor with no readable EDID"
		}
		fmt.Printf("%s: %s has %s\n", DriverName, output.Connector, monitor)
	}
	// The record states the modes the claims on this machine
	// asked for. It is empty on a pod that has just started, because
	// the volume is the pod's own, and a machine with no consumer left
	// comes up with every screen at the mode its monitor prefers. It
	// carries entries when the kubelet restarts the pod's containers
	// under claims that are still held.
	record, err := readModeRecord(modeRecordPath)
	if err != nil {
		fatal("%v", err)
	}
	if err := writeModeRecord(modeRecordPath, record); err != nil {
		fatal("writing %s: %v", modeRecordPath, err)
	}
	if err := writeWestonConfig(westonConfigPath, outputs, record); err != nil {
		fatal("writing %s: %v", westonConfigPath, err)
	}
}

// CompositorProcesses lists the processes running weston under
// one /proc.
//
// The pod shares one process namespace, so this operator sees
// the compositor's container and finds its process by the binary it
// runs. Nothing else in the pod runs weston, and the operator's own
// binary is a different path, so the exe link is the whole test. The
// root is a parameter so a test drives the search over a directory it
// built.
func compositorProcesses(procRoot string) []int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		// /proc holds the kernel's own files beside the numbered
		// directories, and self is a link to the caller's own. A name
		// that is not a number names no process.
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		// The link resolves to the path the process execed, so a
		// container that does not have weston at that path still reads
		// the name. A process that exits between the listing and the
		// read answers an error and counts as gone.
		binary, err := os.Readlink(filepath.Join(procRoot, entry.Name(), "exe"))
		if err != nil || binary != westonBinary {
			continue
		}
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	return pids
}

// EndCompositor sends SIGTERM to the compositor and lets the
// kubelet restart it.
//
// The signal is the whole mechanism. The compositor's container
// holds one process, its exit is the container's exit, and the kubelet
// restarts a container that exited. Nothing in this pod supervises
// another process.
//
// A search that found nothing is a failure to report. A prepare
// that waited for a mode change nothing started would hold the pod
// until its timeout with no reason a person can read.
func endCompositor(procRoot string) error {
	pids := compositorProcesses(procRoot)
	if len(pids) == 0 {
		return fmt.Errorf("no process under %s runs %s", procRoot, westonBinary)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("signaling %s at pid %d: %w", westonBinary, pid, err)
		}
	}
	return nil
}

// compose runs the compositor in place of this process.
//
// The binary finds the card the claim delivered, which no manifest
// can name, then execs weston, so the container holds one process
// and its exit is the exit the kubelet acts on.
//
// Between the two it blocks until one of the card's connectors has a
// monitor, because weston exits at once on a card with none. The
// container is Running for the whole of that wait, so a machine with
// no monitor shows no restarts.
func compose() {
	card := claimedCard()
	socketDir := envOr("SOCKET_DIR", defaultSocketDir)

	// The declare container has already exited when this one starts,
	// so the config is normally there on the first look. The bound is
	// for the file that never arrives, which is a failure to report,
	// not a wait to hold.
	if err := waitForFile(context.Background(), westonConfigPath, configWaitTimeout); err != nil {
		fatal("%v", err)
	}

	// The listener opens before the wait reads sysfs, so a monitor
	// that arrives between the read and the listen is not missed. A
	// listener that cannot open ends the container, because a wait
	// with no wake would hold weston back forever. The listener stops
	// as soon as the wait ends: the wait is its one reader, and
	// weston subscribes to the same events itself.
	listening, stopListening := context.WithCancel(context.Background())
	uevents, err := listenForUevents(listening)
	if err != nil {
		fatal("watching for kernel events: %v", err)
	}
	if _, err := waitForMonitor(listening, sysRoot, card, uevents); err != nil {
		fatal("%v", err)
	}
	stopListening()

	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		fatal("making %s: %v", socketDir, err)
	}

	// libwayland creates the socket with the process umask and never
	// chmods it. A umask of 022 leaves the socket 0755, and connect()
	// needs write permission, so a client running under another uid is
	// refused.
	//
	// The umask survives the exec, and this process creates nothing
	// else before it, so nothing needs to restore it.
	unix.Umask(0)
	fmt.Printf("%s: the compositor takes %s\n", DriverName, card)
	if err := syscall.Exec(westonBinary, westonArgv(card, westonConfigPath, socketName),
		westonEnvironment(os.Environ(), socketDir)); err != nil {
		fatal("running %s: %v", westonBinary, err)
	}
}

// westonArgv builds the compositor's command line.
//
// The card is the name the claim delivered, not a path, because
// weston's --drm-device takes the card's name and looks it up
// itself. The card and the socket name come from this binary rather
// than the manifest, because neither is a fact a deployment can
// know.
func westonArgv(card, configPath, socketName string) []string {
	return []string{
		westonBinary,
		"--backend=drm",
		"--drm-device=" + card,
		"--config=" + configPath,
		"--socket=" + socketName,
	}
}

// westonEnvironment builds the compositor's environment from the
// container's own.
//
// The compositor needs three settings the container's environment
// does not carry: the launcher backend, the hotplug shim, and the
// socket directory. The comments below say why each exists.
func westonEnvironment(environ []string, socketDir string) []string {
	return append(append([]string{}, environ...),
		// Weston's only launcher is libseat, and noop is the only
		// libseat backend that needs neither seatd, nor logind, nor a
		// VT. It opens the device path with a plain open(), which is
		// all a container can do, and the kernel hands DRM master to
		// the first process to open the card with no capability check.
		// libseat never selects noop on its own, so ask for it by
		// name.
		"LIBSEAT_BACKEND=noop",
		// Weston subscribes to hotplug events on the netlink group
		// that only udevd broadcasts on, and liken runs no udevd. The
		// preloaded shim moves that subscription to the kernel's own
		// netlink group, which carries the same events. The variable
		// goes on the compositor alone: the operator's binary is
		// static and loads no libraries.
		"LD_PRELOAD="+hotplugShim,
		// Weston creates the socket in XDG_RUNTIME_DIR, and this is
		// the directory a consumer's container mounts.
		"XDG_RUNTIME_DIR="+socketDir,
	)
}

// waitForFile blocks until the file exists, until the context ends, or
// until the timeout runs out.
//
// A new file raises no event a program can wait on without another
// dependency, so this is a bounded poll on one path, and the startup
// ordering already makes it short.
func waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(filePollInterval)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("nothing created %s within %s", path, timeout)
		case <-tick.C:
		}
	}
}

// compositorServing reports whether a compositor accepts connections
// on the socket.
//
// The check connects and closes rather than stats. A compositor
// that died uncleanly leaves its socket file behind, and a file
// nothing listens on refuses every client, so the refused connect is
// the truth a stat would miss. The socket is the whole delivery, so
// what a client would meet is what the operator answers prepare
// calls and taints the slice by.
func compositorServing(socketPath string) bool {
	connection, err := net.DialTimeout("unix", socketPath, socketDialTimeout)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
