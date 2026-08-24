package main

// These tests cover the idle command sidecar: the dialog that waits for
// mpv's reply to each command, the recreate it runs on a re-present, and
// the identity it takes from its commands topic.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// call reads mpv's reply before it returns, skipping the event lines
// that share the socket, so the command is proven to have run before the
// caller writes the next one or closes the connection.
func TestDialogCallSkipsEventsAndReadsTheReply(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		reader := bufio.NewReader(server)
		_, _ = reader.ReadBytes('\n')
		fmt.Fprintln(server, `{"event":"idle"}`)
		fmt.Fprintln(server, `{"error":"success"}`)
	}()

	d := &mpvDialog{conn: client, reader: bufio.NewReader(client)}
	mustSucceed(t, d.call([]any{"set", "force-window", "no"}))
}

// A reply other than success is the command's failure, so a caller that
// sequences commands stops at the first one mpv refused.
func TestDialogCallReturnsTheReplysError(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		reader := bufio.NewReader(server)
		_, _ = reader.ReadBytes('\n')
		fmt.Fprintln(server, `{"error":"invalid parameter"}`)
	}()

	d := &mpvDialog{conn: client, reader: bufio.NewReader(client)}
	mustFail(t, d.call([]any{"set", "force-window", "maybe"}))
}

// The sidecar's bus identity comes from its commands topic, so one
// identity per Player falls out and two idle sidecars never collide.
func TestIdleCommandClientIDDerivesFromTheTopic(t *testing.T) {
	topic := playerCommandsTopic(defaultTopicBase, "house", "theater")
	mustMatch(t, idleCommandClientID(topic), "idle-command-liken-media-players-house-theater-commands")
}

// A re-present message dials the idle mpv, writes the two force-window
// sets, and then says the surface is on screen, so the clock shows again
// after a Play ends and the display starts the mark in motion on the frame
// it came back into view.
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

	got := readAndReply(t, conn, 3)
	want := []string{
		`{"command":["set","force-window","no"]}`,
		`{"command":["set","force-window","yes"]}`,
		`{"command":["script-message","revealed"]}`,
	}
	mustMatchAll(t, got, want)
}

// A status message reaches the display script as one script-message with
// the operator's JSON as its single argument, so the Lua decodes what the
// operator published and this sidecar reads none of it.
func TestIdleCommandHandleForwardsTheStatusToTheDisplay(t *testing.T) {
	useDialDelay(t, time.Millisecond)
	path := filepath.Join(t.TempDir(), "mpv.sock")
	useSocket(t, path)
	listener, err := net.Listen("unix", path)
	mustSucceed(t, err)
	t.Cleanup(func() { listener.Close() })

	ic := &idleCommander{
		commandsTopic: playerCommandsTopic(defaultTopicBase, "house", "theater"),
		statusTopic:   playerStatusTopic(defaultTopicBase, "house", "theater"),
		runCtx:        context.Background(),
	}
	go ic.handle(ic.statusTopic, []byte(`{"displayName":"Studio Lab","activity":"Idle"}`))

	conn, err := listener.Accept()
	mustSucceed(t, err)
	t.Cleanup(func() { conn.Close() })

	got := readAndReply(t, conn, 1)
	want := []string{
		`{"command":["script-message","player-status","{\"displayName\":\"Studio Lab\",\"activity\":\"Idle\"}"]}`,
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
// deadline, so a writer that sends too few does not hang the test.
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

// readAndReply reads n command lines the way readLines does and answers
// each with mpv's success reply. The replies matter: the idle sidecar's
// dialog waits for each one before its next write, so a silent fake
// would deadlock the sequence under test.
func readAndReply(t *testing.T, conn net.Conn, n int) []string {
	t.Helper()
	mustSucceed(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	scanner := bufio.NewScanner(conn)
	var lines []string
	for len(lines) < n && scanner.Scan() {
		lines = append(lines, scanner.Text())
		fmt.Fprintln(conn, `{"error":"success"}`)
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
