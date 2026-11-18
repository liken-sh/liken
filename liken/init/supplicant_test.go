package main

// The supervision loop restarts a supplicant that nothing else on the
// machine watches. These tests script the failures that the loop must
// survive: a process that dies, a start that fails, and an event
// stream that does not reattach. A machine with a radio cannot be made
// to fail in these ways on purpose.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

// scriptedStarts replaces startSupplicant, so a test can make starts
// fail without a real program. The first `failures` calls return an
// error, and each later call returns a fake process.
type scriptedStarts struct {
	mu       sync.Mutex
	attempts int
	failures int
	pids     chan int
	signals  *signals
	// exitAtOnce records the death of each started process as soon as
	// it starts, before anything awaits it.
	exitAtOnce bool
}

// scriptStarts installs a scriptedStarts until the test ends.
func scriptStarts(t *testing.T, failures int) *scriptedStarts {
	t.Helper()
	s := &scriptedStarts{failures: failures, pids: make(chan int, 16), signals: &signals{}}
	orig := startSupplicant
	startSupplicant = func(ifname, config string) (runningSupplicant, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.attempts++
		if s.failures > 0 {
			s.failures--
			return runningSupplicant{}, fmt.Errorf("starting the supplicant on %s: no such file", ifname)
		}
		// The handle records signals instead of sending them, so no
		// real process with this pid is signalled.
		pid := 1_000_000 + s.attempts
		s.pids <- pid
		proc := s.process(pid)
		if s.exitAtOnce {
			deaths.record(pid, 0)
		}
		return proc, nil
	}
	t.Cleanup(func() { startSupplicant = orig })
	return s
}

// process returns a fake supplicant with the given pid. It marks the
// pid with deaths.expect, as deaths.start does for a real supplicant,
// because the death registry stores a death only for an expected pid.
func (s *scriptedStarts) process(pid int) runningSupplicant {
	deaths.expect(pid)
	return runningSupplicant{pid: pid, signal: func(os.Signal) error {
		s.signals.record(pid)
		return nil
	}}
}

// started waits up to 5 seconds for the next pid that the loop
// started.
func (s *scriptedStarts) started(t *testing.T) int {
	t.Helper()
	select {
	case pid := <-s.pids:
		return pid
	case <-time.After(5 * time.Second):
		t.Fatal("the loop started no supplicant within 5 seconds, want one started")
		return 0
	}
}

func (s *scriptedStarts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// signals records which pids the loop signalled, so a test can check
// which process a stop reached. record also writes the pid's death to
// the death registry, as the reaper does when a real process dies of
// the signal.
type signals struct {
	mu   sync.Mutex
	sent []int
}

func (s *signals) record(pid int) {
	s.mu.Lock()
	s.sent = append(s.sent, pid)
	s.mu.Unlock()
	deaths.record(pid, 0)
}

func (s *signals) reached() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.sent...)
}

// supervised returns a supervision loop with short backoffs. Its
// control client has no socket path, so each test sets one.
func supervised(t *testing.T) *supplicantProcess {
	t.Helper()
	return &supplicantProcess{
		ifname: "wlan0",
		stop:   make(chan struct{}), done: make(chan struct{}),
		backoff: time.Millisecond, maxBackoff: 4 * time.Millisecond,
		control: &wpaControl{out: make(chan wpaEvent, 8)},
	}
}

// end stops the loop and waits up to 5 seconds for it to finish, so no
// test leaves a supervision goroutine running.
func end(t *testing.T, p *supplicantProcess) {
	t.Helper()
	close(p.stop)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the supervision loop did not end within 5 seconds, want it to end")
	}
}

func TestTheSupplicantStartsAgainAfterItDies(t *testing.T) {
	starts := scriptStarts(t, 0)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_001
	go p.run(context.Background(), starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)

	if second := starts.started(t); second == first {
		t.Errorf("the loop waited for the dead process, want a new process started")
	}
	end(t, p)
}

func TestAFailedStartIsTriedAgainUntilOneSucceeds(t *testing.T) {
	// A failed start must not end supervision. The boot continues as if
	// the radio were supervised, so a loop that stopped here would leave
	// a supplicant that nothing restarts.
	starts := scriptStarts(t, 2)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_002
	go p.run(context.Background(), starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)

	live := starts.started(t)
	if live == first {
		t.Fatal("the loop started no new supplicant, want a new one after the first exited")
	}
	if got := starts.count(); got != 3 {
		t.Errorf("the loop tried %d starts, want 3 (two refusals and one that worked)", got)
	}
	end(t, p)
}

func TestAFailedStartNeverSignalsTheProcessThatAlreadyDied(t *testing.T) {
	// A stop after a failed start must not signal a process whose death
	// the loop already collected. Waiting for that pid again never
	// returns, so the stop would hang.
	starts := scriptStarts(t, 1)
	sent := starts.signals
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_003
	go p.run(context.Background(), starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	starts.started(t)

	stopped := make(chan struct{})
	go func() { close(p.stop); <-p.done; close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop did not end within 3 seconds of the stop, want it to end without waiting on a pid that already exited")
	}
	for _, pid := range sent.reached() {
		if pid == first {
			t.Errorf("the stop sent a signal to pid %d, which already exited, want no signal to it", first)
		}
	}
}

func TestStoppingDuringARetryEndsTheLoopWithNothingToSignal(t *testing.T) {
	// Every start fails, so the loop has no process to signal. The stop
	// must still end the loop.
	starts := scriptStarts(t, 1_000)
	sent := starts.signals
	p := supervised(t)
	p.control.socket = servingSocket(t)

	first := 999_004
	go p.run(context.Background(), starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	for starts.count() < 2 {
		time.Sleep(time.Millisecond)
	}
	end(t, p)

	if len(sent.reached()) != 0 {
		t.Errorf("the loop sent signals to %v, want none with no process running", sent.reached())
	}
}

func TestCancellingThePlaneEndsTheLoop(t *testing.T) {
	starts := scriptStarts(t, 0)
	p := supervised(t)
	p.control.socket = servingSocket(t)

	ctx, cancel := context.WithCancel(context.Background())
	first := 999_005
	go p.run(ctx, starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	deaths.record(first, 0)
	starts.started(t)

	cancel()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the loop did not end within 3 seconds of the plane's cancellation, want it to end")
	}
}

func TestTheEventStreamComesBackAfterARestart(t *testing.T) {
	// A supplicant keeps its list of attached clients in memory, so a
	// restarted process sends events to no one until a client attaches.
	// A boot that waits for the radio is waiting for those events, so
	// an attach that fails must be retried, not just logged.
	starts := scriptStarts(t, 0)
	p := supervised(t)

	// The socket does not exist yet, so the first attach fails. It
	// appears later, as when a restarted supplicant finishes starting.
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the control socket directory: %v", err)
	}
	socket := filepath.Join(dir, "wlan0")
	p.control.socket = socket
	p.control.local = filepath.Join(dir, "client")
	p.control.timeout = time.Millisecond

	first := 999_006
	go p.run(context.Background(), starts.process(first), "/run/liken/wireless/wlan0/wpa_supplicant.conf")
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
			t.Fatal("the loop did not attach to the restarted supplicant within 5 seconds, want it attached")
		}
		time.Sleep(5 * time.Millisecond)
	}
	end(t, p)
}

// useWirelessRunDir sets wirelessRunDir to a temporary directory until
// the test ends, and returns it.
func useWirelessRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := wirelessRunDir
	wirelessRunDir = dir
	t.Cleanup(func() { wirelessRunDir = orig })
	return dir
}

// afterTheShutdown closes the supplicant list to new entries, as
// stopSupplicants does, and resets it when the test ends.
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

func trackedSupplicants() []*supplicantProcess {
	supplicantsMu.Lock()
	defer supplicantsMu.Unlock()
	return append([]*supplicantProcess(nil), supplicants...)
}

func TestASupplicantThatStartsAfterTheShutdownIsStoppedAtOnce(t *testing.T) {
	// A radio that connects in the background can call
	// superviseSupplicant after stopSupplicants has run. Nothing would
	// stop that supplicant later, so superviseSupplicant must stop it
	// and not add it to the list.
	useWirelessRunDir(t)
	starts := scriptStarts(t, 0)
	sent := starts.signals
	afterTheShutdown(t)

	dir := controlSocketDir("wlan0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the control socket directory: %v", err)
	}
	serveAt(t, filepath.Join(dir, "wlan0"), "OK\n")

	control, err := superviseSupplicant("wlan0", "/run/liken/wireless/wlan0/wpa_supplicant.conf")
	if err == nil {
		t.Fatal("superviseSupplicant returned no error after the shutdown, want one")
	}
	if control != nil {
		t.Errorf("superviseSupplicant returned the control client %v after the shutdown, want none", control)
	}
	if tracked := trackedSupplicants(); len(tracked) != 0 {
		t.Errorf("%d supplicants are tracked after the refusal, want none", len(tracked))
	}
	pid := starts.started(t)
	if reached := sent.reached(); len(reached) != 1 || reached[0] != pid {
		t.Errorf("the process it started is an orphan: signals reached %v, want [%d]", reached, pid)
	}
}

// servingSocket starts a fake supplicant control socket that replies OK
// to ATTACH, and returns its path.
func servingSocket(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wlan0", "ctrl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the control socket directory: %v", err)
	}
	socket := filepath.Join(dir, "wlan0")
	serveAt(t, socket, "OK\n")
	return socket
}

// serveAt listens on socket until the test ends, and sends answer in
// reply to each ATTACH.
func serveAt(t *testing.T, socket, answer string) {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listening on the control socket: %v", err)
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

func TestStopSupplicantEndsARealProcessThroughItsHandle(t *testing.T) {
	// The stop signals through the os.Process and its pidfd, so the
	// signal can reach only the process that the handle started.
	reapForTest(t)
	cmd := exec.Command("sleep", "60")
	if err := deaths.start(cmd); err != nil {
		t.Fatalf("starting the sleep process: %v", err)
	}
	proc := supplicantHandle(cmd.Process)
	died := make(chan unix.WaitStatus, 1)
	go func() { died <- deaths.await(proc.pid) }()

	stopped := make(chan struct{})
	go func() { stopSupplicant("wlan0", proc, died); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not end within 5 seconds of the stop, want it to end")
	}
}

func TestASupplicantThatNeverAttachesIsStoppedAndAwaited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The supplicant exits at once and never creates its control
		// socket. Its pid is expected, so a death that nothing awaits
		// stays in the death registry, and a later child that reuses the
		// pid, such as k3s, would read that status as its own death.
		useWirelessRunDir(t)
		starts := scriptStarts(t, 0)
		starts.exitAtOnce = true

		begin := time.Now()
		if _, err := superviseSupplicant("wlan0", "/run/liken/wireless/wlan0/wpa_supplicant.conf"); err == nil {
			t.Fatal("superviseSupplicant returned no error for a supplicant that never attached, want one")
		}
		// The socket poll sleeps 50ms between checks, so the refusal
		// comes at the first check after the timeout runs out.
		if elapsed := time.Since(begin); elapsed <= wpaSocketTimeout || elapsed > wpaSocketTimeout+50*time.Millisecond {
			t.Errorf("the refusal came after %s, want the first poll after the timeout of %s", elapsed, wpaSocketTimeout)
		}
		pid := starts.started(t)

		// The next child with the same pid must wait for its own death.
		deaths.expect(pid)
		got := make(chan unix.WaitStatus, 1)
		go func() { got <- deaths.await(pid) }()
		select {
		case status := <-got:
			t.Fatalf("a later child with pid %d read the exited supplicant's status %v, want no status", pid, status)
		case <-time.After(50 * time.Millisecond):
		}
		deaths.record(pid, 0)
		<-got
	})
}
