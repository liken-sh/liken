package main

// The supervision loop answers for a process that nothing else on the
// machine watches. Its three failure paths are a process that dies, a
// start that fails, and an event stream that does not come back. None
// of them can be exercised on a machine with a radio, because none of
// them happens there on purpose, so the tests script the sequences
// through the seams instead.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// scriptedStarts stands in for launching the vendored program. It hands
// out a pid for each start that is meant to succeed, and an error for
// each one that is meant to fail, so a test states the sequence the loop
// must survive rather than arranging for a real program to fail.
type scriptedStarts struct {
	mu       sync.Mutex
	attempts int
	failures int
	pids     chan int
}

// scriptStarts installs the stand-in and fails the given number of
// starts after the first success.
func scriptStarts(t *testing.T, failures int) *scriptedStarts {
	t.Helper()
	s := &scriptedStarts{failures: failures, pids: make(chan int, 16)}
	orig := startSupplicant
	startSupplicant = func(ifname, config string) (int, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.attempts++
		if s.failures > 0 {
			s.failures--
			return 0, fmt.Errorf("starting the supplicant on %s: no such file", ifname)
		}
		// A pid no test can signal: the stand-in below records the
		// signals instead of sending them.
		pid := 1_000_000 + s.attempts
		s.pids <- pid
		return pid, nil
	}
	t.Cleanup(func() { startSupplicant = orig })
	return s
}

// started waits for the next pid the loop launched.
func (s *scriptedStarts) started(t *testing.T) int {
	t.Helper()
	select {
	case pid := <-s.pids:
		return pid
	case <-time.After(5 * time.Second):
		t.Fatal("the loop launched no supplicant")
		return 0
	}
}

func (s *scriptedStarts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// signals records what the loop signalled, so a test can check which
// process a stop reached without any real process being signalled. It
// also plays the reaper's part: a signalled process dies, and the death
// registry is where the loop learns that, exactly as on a machine.
type signals struct {
	mu   sync.Mutex
	sent []int
}

func recordSignals(t *testing.T) *signals {
	t.Helper()
	s := &signals{}
	orig := killProcess
	killProcess = func(pid int, sig unix.Signal) error {
		s.mu.Lock()
		s.sent = append(s.sent, pid)
		s.mu.Unlock()
		deaths.record(pid, 0)
		return nil
	}
	t.Cleanup(func() { killProcess = orig })
	return s
}

func (s *signals) reached() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.sent...)
}

// supervised builds a loop with pacing short enough for a test, and a
// control client attached to a stand-in supplicant that is already
// serving, so an attach always succeeds unless a test says otherwise.
func supervised(t *testing.T) *supplicantProcess {
	t.Helper()
	return &supplicantProcess{
		ifname: "wlan0",
		stop:   make(chan struct{}), done: make(chan struct{}),
		backoff: time.Millisecond, maxBackoff: 4 * time.Millisecond,
		control: &wpaControl{out: make(chan wpaEvent, 8)},
	}
}

// end stops the loop and waits for it, so a test never leaves a
// supervision goroutine behind.
func end(t *testing.T, p *supplicantProcess) {
	t.Helper()
	close(p.stop)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervision loop did not end")
	}
}

func TestTheSupplicantStartsAgainAfterItDies(t *testing.T) {
	starts := scriptStarts(t, 0)
	recordSignals(t)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_001
	go p.run(context.Background(), first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)

	if second := starts.started(t); second == first {
		t.Errorf("the loop must launch a new process, not await the dead one")
	}
	end(t, p)
}

func TestAFailedStartIsTriedAgainUntilOneSucceeds(t *testing.T) {
	// A start that fails once must not end supervision. The boot goes
	// on believing the radio is supervised, so a loop that stops here
	// leaves a machine whose radio nothing will ever restart.
	starts := scriptStarts(t, 2)
	recordSignals(t)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_002
	go p.run(context.Background(), first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)

	live := starts.started(t)
	if live == first {
		t.Fatal("the loop relaunched nothing")
	}
	if got := starts.count(); got != 3 {
		t.Errorf("the loop tried %d starts, want 3: two refusals and the one that worked", got)
	}
	end(t, p)
}

func TestAFailedStartNeverSignalsTheProcessThatAlreadyDied(t *testing.T) {
	// A stop after a failed start must not signal the pid of a process
	// whose death the loop already collected. Waiting on that pid a
	// second time never returns, and the shutdown would abandon the
	// loop at its timeout instead of ending it.
	starts := scriptStarts(t, 1)
	sent := recordSignals(t)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_003
	go p.run(context.Background(), first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	starts.started(t)

	stopped := make(chan struct{})
	go func() { close(p.stop); <-p.done; close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the stop did not end the loop; it is waiting on a pid that already died")
	}
	for _, pid := range sent.reached() {
		if pid == first {
			t.Errorf("the stop signalled %d, the process that already died", first)
		}
	}
}

func TestStoppingDuringARetryEndsTheLoopWithNothingToSignal(t *testing.T) {
	// Every start fails, so the loop is between processes. There is
	// nothing to stop, and the stop must still end the loop at once.
	starts := scriptStarts(t, 1_000)
	sent := recordSignals(t)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_004
	go p.run(context.Background(), first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	for starts.count() < 2 {
		time.Sleep(time.Millisecond)
	}
	end(t, p)

	if len(sent.reached()) != 0 {
		t.Errorf("the loop signalled %v with no process running", sent.reached())
	}
}

func TestCancellingThePlaneEndsTheLoop(t *testing.T) {
	starts := scriptStarts(t, 0)
	recordSignals(t)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	ctx, cancel := context.WithCancel(context.Background())
	first := 999_005
	go p.run(ctx, first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	starts.started(t)

	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("a cancelled plane did not end the loop")
	}
}

func TestTheEventStreamComesBackAfterARestart(t *testing.T) {
	// A supplicant keeps its monitors in memory, so the new process
	// reports to nobody until it is asked. A parked boot waits on
	// exactly those reports, so an attach that fails once must be tried
	// again rather than logged and forgotten.
	starts := scriptStarts(t, 0)
	recordSignals(t)
	p := supervised(t)

	// The socket does not exist yet, so the first attach after the
	// restart fails. It appears part-way through, the way it does when
	// a relaunched supplicant finishes starting.
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "wlan0")
	p.control.socket = socket
	p.control.local = filepath.Join(dir, "client")
	p.control.patience = time.Millisecond

	first := 999_006
	go p.run(context.Background(), first, "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	starts.started(t)

	time.Sleep(20 * time.Millisecond)
	serveAt(t, socket, "OK\n")

	deadline := time.Now().Add(5 * time.Second)
	for {
		p.control.mu.Lock()
		attached := p.control.conn != nil
		p.control.mu.Unlock()
		if attached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the loop never attached to the relaunched supplicant")
		}
		time.Sleep(5 * time.Millisecond)
	}
	end(t, p)
}

// aimWirelessRunDir points the wireless runtime at a tempdir, so a
// test never writes under the machine's own /run.
func aimWirelessRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := wirelessRunDir
	wirelessRunDir = dir
	t.Cleanup(func() { wirelessRunDir = orig })
	return dir
}

// afterTheShutdown latches the supplicant list closed, the way
// stopSupplicants latches it, and restores it when the test ends.
func afterTheShutdown(t *testing.T) {
	t.Helper()
	supplicantsMu.Lock()
	supplicantsStopping = true
	supplicantsMu.Unlock()
	t.Cleanup(func() {
		supplicantsMu.Lock()
		supplicantsStopping = false
		supplicants = nil
		supplicantsMu.Unlock()
	})
}

// trackedSupplicants reports the list the shutdown would stop.
func trackedSupplicants() []*supplicantProcess {
	supplicantsMu.Lock()
	defer supplicantsMu.Unlock()
	return append([]*supplicantProcess(nil), supplicants...)
}

func TestASupplicantThatStartsAfterTheShutdownIsStoppedAtOnce(t *testing.T) {
	// A background radio can reach superviseSupplicant after
	// stopSupplicants has already run. Such a supplicant would never
	// be stopped by anything, so it must be stopped here, and it must
	// never be tracked as if the shutdown could still reach it.
	aimWirelessRunDir(t)
	starts := scriptStarts(t, 0)
	sent := recordSignals(t)
	afterTheShutdown(t)

	dir := controlSocketDir("wlan0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	serveAt(t, filepath.Join(dir, "wlan0"), "OK\n")

	control, err := superviseSupplicant("wlan0", "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	if err == nil {
		t.Fatal("a supplicant started after the shutdown must be refused")
	}
	if control != nil {
		t.Error("a refused supervision hands back no control client")
	}
	if tracked := trackedSupplicants(); len(tracked) != 0 {
		t.Errorf("the refused supplicant is tracked: %d", len(tracked))
	}
	pid := starts.started(t)
	if reached := sent.reached(); len(reached) != 1 || reached[0] != pid {
		t.Errorf("the process it started is an orphan; signals reached %v, want [%d]", reached, pid)
	}
}

// servingSocket is a stand-in supplicant that answers ATTACH, for the
// tests whose subject is the process and not the event stream.
func servingSocket(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "wlan0")
	serveAt(t, socket, "OK\n")
	return socket
}

// serveAt answers ATTACH at one path until the test ends.
func serveAt(t *testing.T, socket, answer string) {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, wpaMessageMax)
		for {
			n, from, err := conn.ReadFromUnix(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == "ATTACH" {
				_, _ = conn.WriteToUnix([]byte(answer), from)
			}
		}
	}()
}
