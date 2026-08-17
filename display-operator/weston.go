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
// writes it: one [output] section for each connected connector, whose
// app-id is the published device's name.
//
// The operator starts weston rather than the entrypoint, because the
// config file has to exist first and the operator is what writes it.
// The two live and die together in both directions: weston that exits
// ends the operator, and an operator that exits ends the container,
// which ends weston.
//
// The flags match what the lab machine runs today, weston 14.0.2 with
// LIBSEAT_BACKEND=noop. That backend opens the device path with a
// plain open(), which is all a container can do, and the kernel hands
// DRM master to the first process to open the card with no capability
// check. So the pod needs no root, no capability, and no seat manager.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// socketWaitTimeout bounds the wait for weston's Wayland socket at
// startup. Weston that never creates it is a failure to report, and
// the pod's restart is the retry.
const socketWaitTimeout = 30 * time.Second

// socketPollInterval is how often the wait looks for the socket. A
// file appearing raises no event that a program can wait on without
// another dependency, so this is the one place that polls, and it
// polls for a bounded time on one path.
const socketPollInterval = 100 * time.Millisecond

// westonConfig builds the weston.ini for one set of outputs.
//
// Every setting here is a requirement of running a compositor in a pod
// on a machine with no keyboard, not a preference a deployment makes,
// so the operator writes the file instead of taking one.
func westonConfig(outputs []Output) string {
	var config strings.Builder
	config.WriteString(`# Written by display.liken.sh at startup. Every edit is lost on the
# next restart of the operator.

[core]
# The kiosk shell makes each client fullscreen on one output and
# routes it there by its app-id, which is what a screen in a house is
# for: one program, no decorations, no desktop.
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
		fmt.Fprintf(&config, `
[output]
name=%s
mode=preferred
app-ids=%s
`, output.Connector, appID(output.Connector))
	}
	return config.String()
}

// routedOutputs is the set of published device names that the
// compositor's config carries an [output] section for. Pass it the
// same outputs that westonConfig received.
//
// This is what the rest of the operator tests a connector against. The
// config is written once, at startup, so a connector that gets its
// first monitor while the operator runs has no section, and a client
// sent to it would land on the first output instead. The set is the
// operator's own record of what it wrote.
func routedOutputs(outputs []Output) map[string]bool {
	routed := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		routed[deviceName(output.Connector)] = true
	}
	return routed
}

// writeWestonConfig writes the compositor's config where weston reads
// it. The directory is the pod's own, not a mount, because the file
// describes the monitors this pod found and no deployment supplies it.
func writeWestonConfig(path string, outputs []Output) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(westonConfig(outputs)), 0o644)
}

// startWeston starts the compositor on one card and returns a channel
// that carries its exit.
//
// The channel is the supervision. Weston holds the screens and the
// socket, so an operator that outlived it would publish outputs that
// no client can draw on, and every client that was drawing has already
// lost its connection. main ends the process on that channel, with a
// nonzero status, and the kubelet restarts the pair.
func startWeston(ctx context.Context, card, configPath, socketDir, socketName string) (<-chan error, error) {
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		return nil, err
	}
	weston := exec.CommandContext(ctx, "weston",
		"--backend=drm",
		"--drm-device="+card,
		"--config="+configPath,
		"--socket="+socketName,
	)
	weston.Env = append(os.Environ(),
		// Weston's only launcher is libseat, and noop is the only
		// libseat backend that needs neither seatd, nor logind, nor a
		// VT. It opens the device path with a plain open(), which is
		// all a container can do, and the kernel hands DRM master to
		// the first process to open the card with no capability check.
		// libseat never selects noop on its own, so ask for it by
		// name.
		"LIBSEAT_BACKEND=noop",
		// Weston creates the socket in XDG_RUNTIME_DIR, and this is
		// the directory a consumer's container mounts.
		"XDG_RUNTIME_DIR="+socketDir,
	)
	weston.Stdout = os.Stdout
	weston.Stderr = os.Stderr
	if err := weston.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() {
		err := weston.Wait()
		if err == nil {
			// A compositor that ends by itself ends every screen, and a
			// zero status does not make that a success. The channel
			// carries a reason either way, so no reader has to treat a
			// nil error as an exit.
			err = errors.New("exit status 0")
		}
		exited <- err
	}()
	return exited, nil
}

// waitForSocket blocks until the compositor's socket accepts
// connections, until the compositor exits, or until the timeout runs
// out.
//
// Nothing may publish before this returns. The delivery a consumer
// receives is the socket, so an output published while the socket is
// missing offers a screen that no client can reach, and the consumer's
// pod would start and fail rather than wait.
//
// The exited channel is what turns a compositor that refuses to start
// into an error now instead of a wait for the whole timeout. A machine
// with no monitor plugged in is the case that meets it.
func waitForSocket(ctx context.Context, path string, timeout time.Duration, exited <-chan error) error {
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
		case err := <-exited:
			return fmt.Errorf("the compositor exited before it created a socket at %s: %v", path, err)
		case <-deadline:
			return fmt.Errorf("the compositor created no socket at %s within %s", path, timeout)
		case <-tick.C:
		}
	}
}
