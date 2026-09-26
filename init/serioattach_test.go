package main

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// fakeLine stands in for an open tty at the syscall boundary. Each
// call records its argument and returns the error the test gave it.
// read returns the scripted errors in order, then blocks until the
// test closes release, which is how a real read behaves until the
// kernel ends the port. The holder calls read on its own locked
// thread, so read also records that thread's blocked-signal mask.
type fakeLine struct {
	termiosErr, disciplineErr, typeErr error
	reads                              []error
	readPanics                         bool
	release                            chan struct{}

	termios    *unix.Termios
	discipline int
	serioType  uint64
	readCalls  int
	sigBlk     uint64
	closed     bool
}

func (f *fakeLine) setTermios(t *unix.Termios) error {
	f.termios = t
	return f.termiosErr
}

func (f *fakeLine) setLineDiscipline(d int) error {
	f.discipline = d
	return f.disciplineErr
}

func (f *fakeLine) setSerioType(t uint64) error {
	f.serioType = t
	return f.typeErr
}

func (f *fakeLine) read() error {
	f.readCalls++
	if f.readCalls == 1 {
		f.sigBlk = threadSigBlk()
	}
	if f.readPanics {
		panic("a fault in the read")
	}
	if len(f.reads) > 0 {
		err := f.reads[0]
		f.reads = f.reads[1:]
		return err
	}
	if f.release != nil {
		<-f.release
	}
	return nil
}

func (f *fakeLine) close() error {
	f.closed = true
	return nil
}

// threadSigBlk reads the calling thread's blocked-signal mask from
// procfs, the kernel's own record of it, so the test checks the mask
// the kernel applies and not a value the code computed.
func threadSigBlk() uint64 {
	file, err := os.Open("/proc/thread-self/status")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "SigBlk:"); ok {
			mask, _ := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
			return mask
		}
	}
	return 0
}

// opener returns an openLine that hands out one fake line, or the
// open error the test names.
func opener(line *fakeLine, err error) openLine {
	return func(string) (serialLine, error) {
		if err != nil {
			return nil, err
		}
		return line, nil
	}
}

var pulse8Protocol, _ = machine.LookupSerioProtocol("pulse8-cec")

// The line settings are inputattach's setline for a 9600-baud, 8-bit
// device, and the two ioctls carry serport's discipline number and the
// table's serio type.
func TestAttachSerioRunsInputattachsSequence(t *testing.T) {
	line := &fakeLine{}

	got, err := attachSerio(opener(line, nil), "/dev/ttyACM0", pulse8Protocol)

	if err != nil || got != line {
		t.Fatalf("attachSerio = %v, %v", got, err)
	}
	want := unix.Termios{
		Cflag:  unix.CS8 | unix.CREAD | unix.HUPCL | unix.CLOCAL | unix.B9600,
		Iflag:  unix.IGNBRK | unix.IGNPAR,
		Ispeed: unix.B9600, Ospeed: unix.B9600,
	}
	want.Cc[unix.VMIN] = 1
	if *line.termios != want {
		t.Errorf("termios = %+v", *line.termios)
	}
	if line.discipline != 2 || line.serioType != 0x40 || line.closed {
		t.Errorf("discipline %d, type %#x, closed %v", line.discipline, line.serioType, line.closed)
	}
}

// Every refusal names the call and carries the kernel's text word for
// word, and a refused line is closed so the tty is not left holding a
// half-set discipline.
func TestAttachSerioNamesTheCallThatFailed(t *testing.T) {
	tests := []struct {
		name    string
		line    *fakeLine
		openErr error
		want    string
		closed  bool
	}{
		{"open", &fakeLine{}, unix.ENOENT, "open /dev/ttyACM0: no such file or directory", false},
		{"the line settings", &fakeLine{termiosErr: unix.EIO}, nil, "TCSETS: input/output error", true},
		{"the discipline", &fakeLine{disciplineErr: unix.EPERM}, nil, "TIOCSETD: operation not permitted", true},
		{"the serio type", &fakeLine{typeErr: unix.EINVAL}, nil, "SPIOCSTYPE: invalid argument", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := attachSerio(opener(test.line, test.openErr), "/dev/ttyACM0", pulse8Protocol)
			if err == nil || err.Error() != test.want {
				t.Errorf("err = %v", err)
			}
			if test.line.closed != test.closed {
				t.Errorf("closed = %v", test.line.closed)
			}
		})
	}
}

func TestAttachSerioRefusesASpeedItHasNoSettingFor(t *testing.T) {
	odd := pulse8Protocol
	odd.Baud = 12345
	line := &fakeLine{}

	_, err := attachSerio(opener(line, nil), "/dev/ttyACM0", odd)

	if err == nil || !strings.Contains(err.Error(), "12345") || !line.closed {
		t.Errorf("err = %v, closed = %v", err, line.closed)
	}
}

// serport's read returns only when the port ends. inputattach repeats
// it on EINTR and EAGAIN, and so does the holder.
func TestHoldReadRepeatsAnInterruptedRead(t *testing.T) {
	tests := []struct {
		name  string
		reads []error
		want  error
		calls int
	}{
		{"the port ends", nil, nil, 1},
		{"an interrupted read", []error{unix.EINTR, unix.EAGAIN}, nil, 3},
		{"a read the kernel refuses", []error{unix.EINTR, unix.EBUSY}, unix.EBUSY, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := &fakeLine{reads: test.reads}
			if err := holdRead(line); !errors.Is(err, test.want) || line.readCalls != test.calls {
				t.Errorf("holdRead = %v after %d reads", err, line.readCalls)
			}
		})
	}
}

// The mask blocks everything that can arrive from outside the thread,
// SIGCHLD above all, and leaves the fault signals open. A fault signal
// that is blocked when the thread faults kills the process, and in
// PID 1 that panics the kernel, where an open one reaches Go's panic
// and the holder's recover.
func TestTheHolderMaskLeavesOnlyTheFaultSignalsOpen(t *testing.T) {
	mask := holderSignalMask()
	// Signal 33 is SIGRTMIN+1, which the Go runtime sends to every
	// thread to run a setuid-family call on each one.
	for _, sig := range []unix.Signal{unix.SIGCHLD, unix.SIGURG, unix.SIGTERM, unix.SIGINT, unix.SIGHUP, unix.SIGPIPE, unix.Signal(33)} {
		if !sigsetHas(&mask, sig) {
			t.Errorf("%v is open", sig)
		}
	}
	for _, sig := range faultSignals {
		if sigsetHas(&mask, sig) {
			t.Errorf("%v is blocked", sig)
		}
	}
}

// startHolder runs one holder against a fake line and a nudge channel,
// and waits for its attach calls to finish.
func startHolder(t *testing.T, line *fakeLine, openErr error) (*serioHolder, chan struct{}) {
	t.Helper()
	nudge := make(chan struct{}, 1)
	h := startSerioHolder(opener(line, openErr), "/dev/ttyACM0", pulse8Protocol, nudge)
	select {
	case <-h.settled:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder never finished its attach calls")
	}
	return h, nudge
}

// waitDone waits for a holder's goroutine to end.
func waitDone(t *testing.T, h *serioHolder) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder never ended")
	}
}

func TestAHolderReadsOnAThreadThatBlocksSignals(t *testing.T) {
	line := &fakeLine{release: make(chan struct{})}
	h, nudge := startHolder(t, line, nil)
	if h.attachErr != nil {
		t.Fatal(h.attachErr)
	}
	close(line.release)
	waitDone(t, h)

	if line.sigBlk&(1<<(unix.SIGCHLD-1)) == 0 {
		t.Errorf("the read ran with SigBlk %#x", line.sigBlk)
	}
	if line.sigBlk&(1<<(unix.SIGSEGV-1)) != 0 {
		t.Errorf("the read ran with SIGSEGV blocked: %#x", line.sigBlk)
	}
	if h.endErr != nil || !line.closed {
		t.Errorf("endErr %v, closed %v", h.endErr, line.closed)
	}
	select {
	case <-nudge:
	default:
		t.Error("an ended holder must wake the walk")
	}
}

func TestAHolderReportsARefusedAttach(t *testing.T) {
	h, nudge := startHolder(t, &fakeLine{disciplineErr: unix.EPERM}, nil)
	waitDone(t, h)

	if h.attachErr == nil || h.attachErr.Error() != "TIOCSETD: operation not permitted" {
		t.Errorf("attachErr = %v", h.attachErr)
	}
	select {
	case <-nudge:
	default:
		t.Error("a refused holder must wake the walk")
	}
}

// A panic in the holder's goroutine is outside the machine plane's
// recovery boundary, so the holder carries its own. Without it, the
// panic would end PID 1.
func TestAHolderRecoversAPanicAsAStatusFact(t *testing.T) {
	h, _ := startHolder(t, &fakeLine{readPanics: true}, nil)
	waitDone(t, h)

	if h.attachErr != nil || h.endErr == nil || !strings.Contains(h.endErr.Error(), "a fault in the read") {
		t.Errorf("attachErr %v, endErr %v", h.attachErr, h.endErr)
	}
}

func TestAPanicInTheReadClosesTheLine(t *testing.T) {
	line := &fakeLine{readPanics: true}
	h, _ := startHolder(t, line, nil)
	waitDone(t, h)

	if !line.closed {
		t.Error("a line left open keeps serport's discipline on the tty")
	}
}

// A second panic, in the close, must not escape the holder's own
// recovery.
func TestAPanicInTheCloseAfterAPanicStaysInside(t *testing.T) {
	closeAfterPanic(panickingClose{})
	closeAfterPanic(nil)
}

type panickingClose struct{ serialLine }

func (panickingClose) close() error { panic("a fault in the close") }

func TestSpiocstypeIsTheKernelsNumber(t *testing.T) {
	// _IOW('q', 0x01, unsigned long) on a 64-bit kernel.
	if spiocstype != 0x40087101 {
		t.Errorf("SPIOCSTYPE = %#x", spiocstype)
	}
}
