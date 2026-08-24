package main

// The idle command sidecar recreates the idle mpv's Wayland surface when
// a Play on the same Player ends. It is a native sidecar beside the idle
// mpv, subscribing to the Player's commands topic and driving mpv through
// the JSON IPC socket the two containers share on the pod's ipc volume.
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

// idleCommander holds the idle command sidecar's one input, the commands
// topic it subscribes to, and the run context every re-present dials mpv
// under.
type idleCommander struct {
	commandsTopic string
	runCtx        context.Context
}

// runIdleCommand connects to the bus, subscribes to the Player's commands
// topic, and recreates the idle surface on each re-present. It returns on
// the kubelet's grace signal. The topic is pre-built and carries the
// Player's identity, so the sidecar subscribes to one exact topic and
// parses nothing.
func runIdleCommand() {
	busAddress := os.Getenv(busAddressVariable)
	commandsTopic := os.Getenv(playerCommandsTopicVariable)

	// The idle command sidecar is its container's PID 1, so the signal
	// context ends the run on the kubelet's SIGTERM. Every re-present dials
	// mpv under it, so a dial in progress ends when the run does.
	runCtx, stopRun := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopRun()

	ic := &idleCommander{commandsTopic: commandsTopic, runCtx: runCtx}
	bus := newBus(busAddress, idleCommandClientID(commandsTopic), nil, nil, ic.handle)
	// The subscription is made once. The Bus remembers the filter and
	// re-sends it on every reconnect, so a broker restart does not need the
	// sidecar to subscribe again.
	bus.Subscribe(commandsTopic)
	bus.Run(runCtx)
}

// idleCommandClientID builds the sidecar's bus identity from its commands
// topic. The topic carries the Player's namespace and name, so one
// identity per Player falls out with no extra environment, and two idle
// sidecars never collide on the broker.
func idleCommandClientID(commandsTopic string) string {
	return "idle-command-" + strings.ReplaceAll(commandsTopic, "/", "-")
}

// handle turns one commands message into a re-present. The topic is
// fixed, so the payload alone matters: it decodes to a named command, and
// only the re-present action recreates the surface. A payload that does
// not decode, or any other action, does nothing, so a newer command on
// the topic has no effect rather than a crash.
func (ic *idleCommander) handle(topic string, payload []byte) {
	var command mediaCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return
	}
	if command.Action != actionRePresent {
		return
	}
	ic.represent()
}

// represent dials the idle mpv and recreates its surface. A dial that
// does not connect within idleDialTimeout writes nothing, so a re-present
// that lands while the idle mpv restarts does not hold the bus reader.
func (ic *idleCommander) represent() {
	ctx, cancel := context.WithTimeout(ic.runCtx, idleDialTimeout)
	defer cancel()
	conn, err := dialMPV(ctx, mpvSocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: reach mpv: %v\n", err)
		return
	}
	defer conn.Close()
	if err := recreateSurface(conn); err != nil {
		fmt.Fprintf(os.Stderr, "idle-command: recreate the idle surface: %v\n", err)
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
