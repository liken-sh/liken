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

	"github.com/liken-sh/liken/liken/machine"
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
	readPanics, closePanics            bool
	release                            chan struct{}

	termios    *unix.Termios
	discipline int
	serioType  uint64
	readCalls  int
	sigBlk     uint64
	closed     bool
	closeCalls int
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
	f.closeCalls++
	if f.closePanics {
		panic("a fault in the close")
	}
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
// device, and the two ioctls pass serport's discipline number and the
// table's serio type.
func TestAttachSerioRunsInputattachsSequence(t *testing.T) {
	line := &fakeLine{}

	got, err := attachSerio(opener(line, nil), "/dev/ttyACM0", pulse8Protocol)

	if err != nil || got != line {
		t.Fatalf("attachSerio returned %v, %v, want the open line and no error", got, err)
	}
	want := unix.Termios{
		Cflag:  unix.CS8 | unix.CREAD | unix.HUPCL | unix.CLOCAL | unix.B9600,
		Iflag:  unix.IGNBRK | unix.IGNPAR,
		Ispeed: unix.B9600, Ospeed: unix.B9600,
	}
	want.Cc[unix.VMIN] = 1
	if *line.termios != want {
		t.Errorf("the termios are %+v, want %+v", *line.termios, want)
	}
	if line.discipline != 2 || line.serioType != 0x40 || line.closed {
		t.Errorf("the discipline is %d, the type is %#x, and closed is %v, want 2, 0x40, and false", line.discipline, line.serioType, line.closed)
	}
}

// Each error starts with the failed call and keeps the kernel's text.
// A line that fails after the open is closed, so the tty does not keep
// a partly set discipline.
func TestAttachSerioNamesTheCallThatFailed(t *testing.T) {
	tests := []struct {
		name    string
		line    *fakeLine
		openErr error
		want    string
		closed  bool
	}{
		{"open", &fakeLine{}, unix.ENOENT, "opening /dev/ttyACM0: no such file or directory", false},
		{"the line settings", &fakeLine{termiosErr: unix.EIO}, nil, "setting the line speed and mode (TCSETS): input/output error", true},
		{"the discipline", &fakeLine{disciplineErr: unix.EPERM}, nil, "setting the serport line discipline (TIOCSETD): operation not permitted", true},
		{"the serio type", &fakeLine{typeErr: unix.EINVAL}, nil, "setting the serio type (SPIOCSTYPE): invalid argument", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := attachSerio(opener(test.line, test.openErr), "/dev/ttyACM0", pulse8Protocol)
			if err == nil || err.Error() != test.want {
				t.Errorf("the error is %v, want %q", err, test.want)
			}
			if test.line.closed != test.closed {
				t.Errorf("closed is %v, want %v", test.line.closed, test.closed)
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
		t.Errorf("the error is %v and closed is %v, want an error that names 12345 and true", err, line.closed)
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
				t.Errorf("holdRead returned %v after %d reads, want %v after %d", err, line.readCalls, test.want, test.calls)
			}
		})
	}
}

// The mask blocks every signal that can arrive from outside the
// thread, and leaves the fault signals unblocked (see faultSignals).
func TestTheHolderMaskLeavesOnlyTheFaultSignalsOpen(t *testing.T) {
	mask := holderSignalMask()
	// Signal 33 is SIGRTMIN+1, which the Go runtime sends to every
	// thread to run a setuid-family call on each one.
	for _, sig := range []unix.Signal{unix.SIGCHLD, unix.SIGURG, unix.SIGTERM, unix.SIGINT, unix.SIGHUP, unix.SIGPIPE, unix.Signal(33)} {
		if !sigsetHas(&mask, sig) {
			t.Errorf("%v is open in the mask, want blocked", sig)
		}
	}
	for _, sig := range faultSignals {
		if sigsetHas(&mask, sig) {
			t.Errorf("%v is blocked in the mask, want open", sig)
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
		t.Fatal("the holder did not finish its attach calls within 5 seconds, want them finished")
	}
	return h, nudge
}

// waitDone waits for a holder's goroutine to end.
func waitDone(t *testing.T, h *serioHolder) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the holder did not end within 5 seconds, want it to end")
	}
}

// waitNudge waits for the holder's send on nudge. The holder closes
// done before it sends, so the send can come after waitDone returns.
func waitNudge(t *testing.T, nudge <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-nudge:
	case <-time.After(5 * time.Second):
		t.Error(msg)
	}
}

func TestAHolderReadsOnAThreadThatBlocksSignals(t *testing.T) {
	line := &fakeLine{release: make(chan struct{})}
	h, nudge := startHolder(t, line, nil)
	if h.attachErr != nil {
		t.Fatalf("attaching the line: %v", h.attachErr)
	}
	close(line.release)
	waitDone(t, h)

	if line.sigBlk&(1<<(unix.SIGCHLD-1)) == 0 {
		t.Errorf("SigBlk during the read is %#x, want SIGCHLD blocked", line.sigBlk)
	}
	if line.sigBlk&(1<<(unix.SIGSEGV-1)) != 0 {
		t.Errorf("SigBlk during the read is %#x, want SIGSEGV open", line.sigBlk)
	}
	if h.endErr != nil || !line.closed {
		t.Errorf("endErr is %v and closed is %v, want nil and true", h.endErr, line.closed)
	}
	waitNudge(t, nudge, "an ended holder must wake the walk")
}

func TestAHolderReportsARefusedAttach(t *testing.T) {
	h, nudge := startHolder(t, &fakeLine{disciplineErr: unix.EPERM}, nil)
	waitDone(t, h)

	if h.attachErr == nil || h.attachErr.Error() != "setting the serport line discipline (TIOCSETD): operation not permitted" {
		t.Errorf("attachErr is %v, want the TIOCSETD error", h.attachErr)
	}
	waitNudge(t, nudge, "a refused holder must wake the walk")
}

// The machine plane does not recover a panic in the holder's
// goroutine, so the holder must, or the panic would end PID 1.
func TestAHolderRecoversAPanicAsItsEndError(t *testing.T) {
	h, _ := startHolder(t, &fakeLine{readPanics: true}, nil)
	waitDone(t, h)

	if h.attachErr != nil || h.endErr == nil || !strings.Contains(h.endErr.Error(), "a fault in the read") {
		t.Errorf("attachErr is %v and endErr is %v, want nil and an error that holds the panic text", h.attachErr, h.endErr)
	}
}

func TestAPanicInTheReadClosesTheLine(t *testing.T) {
	line := &fakeLine{readPanics: true}
	h, _ := startHolder(t, line, nil)
	waitDone(t, h)

	if !line.closed {
		t.Error("the line is open after a panic in the read, want it closed")
	}
}

// A panic in the close after the read is recovered, and the recover
// does not close the line a second time.
func TestAPanicInTheFinalCloseClosesOnce(t *testing.T) {
	line := &fakeLine{closePanics: true}
	h, _ := startHolder(t, line, nil)
	waitDone(t, h)

	if line.closeCalls != 1 || h.endErr == nil {
		t.Errorf("close ran %d times and endErr is %v, want 1 time and an error", line.closeCalls, h.endErr)
	}
}

// A panic in closeAfterPanic's close must not escape it.
func TestAPanicInTheCloseAfterAPanicStaysInside(t *testing.T) {
	closeAfterPanic(panickingClose{})
	closeAfterPanic(nil)
}

type panickingClose struct{ serialLine }

func (panickingClose) close() error { panic("a fault in the close") }

func TestSpiocstypeIsTheKernelsNumber(t *testing.T) {
	// _IOW('q', 0x01, unsigned long) on a 64-bit kernel.
	if spiocstype != 0x40087101 {
		t.Errorf("SPIOCSTYPE is %#x, want 0x40087101", spiocstype)
	}
}
