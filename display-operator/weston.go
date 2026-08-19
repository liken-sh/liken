package main

// Running the compositor.
//
// Weston is the daemon this operator runs, in the pattern's sense: it
// holds the hardware that the operator's own claim acquired, and what
// it holds is what the operator publishes. DRM master is one per card,
// so exactly one process may set a mode on the card, and the exclusive
// display claim is what makes weston that process.
//
// The kiosk shell is the shell for a screen in a house. It makes every
// client fullscreen on one output, with no decorations and no desktop,
// and it routes a client to an output by the app-id the client sets.
// weston.ini's app-ids= line is the routing table, and this operator
// writes it: one [output] section for each connector, whose app-id is
// the published device's name.
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

// socketPollInterval is how often a wait looks for a file. A
// new file raises no event that a program can wait on without
// another dependency.
//
// The compositor role's wait for its config is this interval's one
// reader, and that wait ends within a tick or two, because the
// declare container exits before this one starts.
const socketPollInterval = 100 * time.Millisecond

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
// monitor arrives, that section configures and routes the new output.
func westonConfig(outputs []Output, modes map[string]string) string {
	var config strings.Builder
	config.WriteString(`# Written by display.liken.sh at startup. Every edit is lost on the
# next restart of the operator.

[core]
# The kiosk shell makes each client fullscreen on one output, with
# no decorations and no desktop, and routes it there by its app-id.
shell=kiosk

# The GL renderer advertises zwp_linux_dmabuf_v1 at version 4. mpv
# refuses to bind the protocol below version 4, and the pixman
# renderer publishes no dmabuf feedback, so it advertises version 3
# and mpv falls back to software paths.
renderer=gl

# A machine with monitors on it has no keyboard and no mouse. Weston
# otherwise refuses to start when it finds no input device.
require-input=false

# 0 turns the idle timeout off. The default of 300 seconds fades the
# screens to black, and with no input device nothing ever wakes them.
idle-time=0
`)
	for _, output := range outputs {
		mode := preferredMode
		if stated := modes[output.Connector]; stated != "" {
			mode = stated
		}
		fmt.Fprintf(&config, `
[output]
name=%s
mode=%s
app-ids=%s
`, output.Connector, mode, appID(output.Connector))
	}
	return config.String()
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
		// Every connector still publishes, tainted, so a person can
		// claim a screen that is cabled and asleep and the pod parks
		// until somebody wakes it. The operator does not test whether
		// the compositor starts with no output: a compositor that
		// refuses exits, and that exit is the report.
		fmt.Fprintf(os.Stderr, "%s has no monitor on any of its %d connectors\n", card, len(outputs))
	}
	for _, output := range live {
		monitor := monitorID(output.Monitor)
		if monitor == "" {
			monitor = "a monitor with no readable EDID"
		}
		fmt.Printf("%s: %s has %s, app-id %s\n",
			DriverName, output.Connector, monitor, appID(output.Connector))
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
	tick := time.NewTicker(socketPollInterval)
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
