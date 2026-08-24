package main

// These tests cover the idle command sidecar: the two force-window sets
// that recreate the idle surface, the identity it takes from its commands
// topic, and the handler that runs the recreate only on a re-present.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// recreateSurface clears force-window and sets it again, in that order,
// so mpv destroys the surface and then builds a fresh one that a seatless
// kiosk shell reveals.
func TestRecreateSurfaceIssuesTheTwoForceWindowSetsInOrder(t *testing.T) {
	fastTeardown(t)
	var buffer bytes.Buffer

	mustSucceed(t, recreateSurface(&buffer))

	got := splitLines(buffer.String())
	want := []string{
		`{"command":["set","force-window","no"]}`,
		`{"command":["set","force-window","yes"]}`,
	}
	mustMatchAll(t, got, want)
}

// The sidecar's bus identity comes from its commands topic, so one
// identity per Player falls out and two idle sidecars never collide.
func TestIdleCommandClientIDDerivesFromTheTopic(t *testing.T) {
	topic := playerCommandsTopic(defaultTopicBase, "house", "theater")
	mustMatch(t, idleCommandClientID(topic), "idle-command-liken-media-players-house-theater-commands")
}

// A re-present message dials the idle mpv and writes the two force-window
// sets, so the clock shows again after a Play ends.
func TestIdleCommandHandleRecreatesTheSurfaceOnRePresent(t *testing.T) {
	fastTeardown(t)
	useDialDelay(t, time.Millisecond)
	path := filepath.Join(t.TempDir(), "mpv.sock")
	useSocket(t, path)
	listener, err := net.Listen("unix", path)
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })

	ic := &idleCommander{
		commandsTopic: playerCommandsTopic(defaultTopicBase, "house", "theater"),
		runCtx:        context.Background(),
	}
	payload, err := json.Marshal(mediaCommand{Action: actionRePresent})
	mustSucceed(t, err)
	go ic.handle(ic.commandsTopic, payload)

	conn, err := listener.Accept()
	mustSucceed(t, err)
	t.Cleanup(func() { conn.Close() })

	got := readLines(t, conn, 2)
	want := []string{
		`{"command":["set","force-window","no"]}`,
		`{"command":["set","force-window","yes"]}`,
	}
	mustMatchAll(t, got, want)
}

// Any action other than re-present, and a payload that does not decode,
// reach mpv with nothing, so the sidecar makes no contact for a command
// it does not act on.
func TestIdleCommandHandleActsOnlyOnRePresent(t *testing.T) {
	useDialDelay(t, time.Millisecond)
	path := filepath.Join(t.TempDir(), "mpv.sock")
	useSocket(t, path)
	listener, err := net.Listen("unix", path)
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })

	ic := &idleCommander{runCtx: context.Background()}
	pause, err := json.Marshal(mediaCommand{Action: actionPause})
	mustSucceed(t, err)
	ic.handle("topic", pause)
	ic.handle("topic", []byte("not json"))

	listener.(*net.UnixListener).SetDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = listener.Accept()
	mustFail(t, err)
}

// readLines reads n newline-delimited lines from the connection, under a
// deadline, so a sidecar that writes too few does not hang the test.
func readLines(t *testing.T, conn net.Conn, n int) []string {
	t.Helper()
	mustSucceed(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	scanner := bufio.NewScanner(conn)
	var lines []string
	for len(lines) < n && scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// splitLines renders the JSON commands one per line, dropping the
// trailing empty field the last newline leaves.
func splitLines(text string) []string {
	scanner := bufio.NewScanner(bytes.NewReader([]byte(text)))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// fastTeardown shrinks the surface-teardown gap for the length of one
// test, so the two writes do not wait the production 200ms.
func fastTeardown(t *testing.T) {
	t.Helper()
	was := surfaceTeardownGap
	t.Cleanup(func() { surfaceTeardownGap = was })
	surfaceTeardownGap = time.Millisecond
}
