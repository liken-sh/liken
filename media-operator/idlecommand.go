package main

// The idle command sidecar is the idle screen's one path to the bus. It
// recreates the idle mpv's Wayland surface when a Play on the same Player
// ends, and it forwards the unit's live state into the display script. It
// is a native sidecar beside the idle mpv, subscribing to the Player's
// commands and status topics and driving mpv through the JSON IPC socket
// the two containers share on the pod's ipc volume.
//
// The recreate is the whole fix for a seatless compositor. Weston's
// kiosk-shell reveals a lower surface only along a code path gated on a
// seat, and liken's compositor runs with require-input=false and no
// input devices, so it has no seat. When a Play's surface is destroyed
// the idle clock stays hidden and the screen goes black, though the idle
// mpv still runs. A freshly mapped surface is revealed along a
// seat-independent path, so the sidecar makes the idle mpv destroy and
// recreate its surface, and kiosk shows the fresh one. The idle mpv
// keeps running across the cycle, so the gap is sub-second and no pod
// restarts.
//
// The sidecar holds no API credentials and reaches the control plane
// only through the operator's own place on the bus.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// surfaceTeardownGap is the pause between clearing force-window and
// setting it again. mpv's video output destroys the surface on the first
// set and builds a new one on the second, and the gap lets the surface
// fully destroy before it is recreated, so the two do not race inside the
// video output. 200ms is well above the teardown a local Wayland surface
// needs and far below what a person notices as a gap in the clock.
var surfaceTeardownGap = 200 * time.Millisecond

// idleDialTimeout bounds one re-present's wait for the idle mpv socket.
// The idle mpv has run since the pod started, so the dial connects at
// once in the ordinary case. The timeout only limits the rare case of a
// re-present that arrives while the idle mpv restarts after a crash, so
// the bus reader is not held on a socket that is not coming back.
var idleDialTimeout = 5 * time.Second

// The two script messages the sidecar broadcasts into the idle display
// script. player-status carries one status payload as its single argument,
// and revealed says the idle surface is on screen again, which is the
// frame the display starts the mark's ramp-down from.
const (
	playerStatusMessage = "player-status"
	revealedMessage     = "revealed"
)

// idleCommander holds the idle command sidecar's two inputs, the commands
// and status topics it subscribes to, and the run context every write to
// mpv dials under.
type idleCommander struct {
	commandsTopic string
	statusTopic   string
	runCtx        context.Context
}

// runIdleCommand connects to the bus, subscribes to the Player's commands
// and status topics, recreates the idle surface on each re-present, and
// forwards each status into the display script. It returns on the
// kubelet's grace signal. Both topics are pre-built and carry the Player's
// identity, so the sidecar subscribes to two exact topics and parses
// nothing.
func runIdleCommand() {
	busAddress := os.Getenv(busAddressVariable)
	commandsTopic := os.Getenv(playerCommandsTopicVariable)
	statusTopic := os.Getenv(playerStatusTopicVariable)

	// The idle command sidecar is its container's PID 1, so the signal
	// context ends the run on the kubelet's SIGTERM. Every re-present dials
	// mpv under it, so a dial in progress ends when the run does.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	ic := &idleCommander{commandsTopic: commandsTopic, statusTopic: statusTopic, runCtx: runCtx}
	bus := newBus(busAddress, idleCommandClientID(commandsTopic), nil, nil, ic.handle)
	// Each subscription is made once. The Bus remembers the filters and
	// re-sends them on every reconnect, so a broker restart does not need
	// the sidecar to subscribe again. The status topic is retained, so the
	// broker delivers the current state on this subscribe and a pod that
	// just started paints live state with no request of its own.
	bus.Subscribe(commandsTopic)
	bus.Subscribe(statusTopic)
	bus.Run(runCtx)
}

// idleCommandClientID builds the sidecar's bus identity from its commands
// topic. The topic carries the Player's namespace and name, so one
// identity per Player falls out with no extra environment, and two idle
// sidecars never collide on the broker.
func idleCommandClientID(commandsTopic string) string {
	return "idle-command-" + strings.ReplaceAll(commandsTopic, "/", "-")
}

// handle folds one message from either subscription. The two topics carry
// different messages, so the topic says which this is: the status topic
// carries the unit's state, and the commands topic carries a named
// command, of which only the re-present acts. A payload that does not
// decode, or any other action, does nothing, so a newer command on the
// topic has no effect rather than a crash.
//
// The Bus calls this on its reader goroutine, so the two topics are served
// in the order the broker delivered them. The operator publishes a Player's
// status before the re-present that follows it, so the display reads the
// Idle status before the reveal and animates the return.
func (ic *idleCommander) handle(topic string, payload []byte) {
	if topic == ic.statusTopic {
		ic.forwardStatus(payload)
		return
	}
	var command mediaCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return
	}
	if command.Action != actionRePresent {
		return
	}
	ic.represent()
}

// forwardStatus hands one status message to the display script as the
// player-status script message. The payload travels as one string
// argument, so the Lua decodes the same JSON the operator published and
// this sidecar reads none of it: a field the display starts drawing needs
// no change here.
func (ic *idleCommander) forwardStatus(payload []byte) {
	ic.withMPV("forward the player status", func(conn io.Writer) error {
		return sendCommand(conn, []any{"script-message", playerStatusMessage, string(payload)})
	})
}

// represent recreates the idle surface and then reports that it is on
// screen. The reveal goes out on the same connection and after the second
// force-window set succeeds, so the display starts the mark in
// motion on the frame the surface came back into view.
func (ic *idleCommander) represent() {
	ic.withMPV("recreate the idle surface", func(conn io.Writer) error {
		if err := recreateSurface(conn); err != nil {
			return err
		}
		return sendCommand(conn, []any{"script-message", revealedMessage})
	})
}

// withMPV dials the idle mpv and runs one write against it. A dial that
// does not connect within idleDialTimeout writes nothing, so a message
// that lands while the idle mpv restarts does not hold the bus reader.
// The what argument names the write in the failure line, because every
// caller reaches mpv the same way and only the write differs.
func (ic *idleCommander) withMPV(what string, write func(conn io.Writer) error) {
	ctx, cancel := context.WithTimeout(ic.runCtx, idleDialTimeout)
	defer cancel()
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: reach mpv: %v\n", err)
		return
	}
	defer conn.Close()
	if err := write(conn); err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: %s: %v\n", what, err)
	}
}

// recreateSurface writes the two force-window sets that destroy and
// rebuild the idle mpv's surface. The first clears the window, so mpv's
// video output tears the surface down; the second sets it again, so mpv
// builds a fresh surface that kiosk reveals. The pause between them is
// surfaceTeardownGap, so the teardown finishes before the rebuild starts.
func recreateSurface(conn io.Writer) error {
	if err := sendCommand(conn, []any{"set", "force-window", "no"}); err != nil {
		return err
	}
	time.Sleep(surfaceTeardownGap)
	return sendCommand(conn, []any{"set", "force-window", "yes"})
}
