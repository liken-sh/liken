package main

// Holding one serio attachment: the five system calls that attach a
// serial line to the kernel's serio layer, and the goroutine that
// keeps the last of them blocked for the life of the boot.
//
// The calls are the ones inputattach runs, in its order. The program
// opens the tty, sets the line to the device's speed in raw mode,
// sets serport's line discipline with TIOCSETD, states the serio type
// with SPIOCSTYPE, and reads. serport registers the serio port inside
// that read, and unregisters it when the read returns
// (drivers/input/serio/serport.c, serport_ldisc_read). So the port,
// and every device its driver creates, exists exactly as long as one
// read blocks.
//
// PID 1 makes that read harder to hold than it is for inputattach.
// Init reaps every orphaned process on the machine, so SIGCHLD arrives
// often, and a signal that lands on the reading thread wakes the
// kernel's wait. serport then unregisters the port even though the
// read restarts, and the CEC adapter disappears under every pod that
// holds it. So each holder locks its goroutine to one OS thread and
// blocks signals on that thread before the first call. The kernel
// delivers a process-directed signal to a thread that does not block
// it, so the rest of init still receives SIGCHLD.
//
// A holder must never take PID 1 down. The machine plane's recover
// does not cover a goroutine a component starts, so the holder
// recovers its own panics and reports them as a status fact. It
// shares no map with any other goroutine: it writes only its own
// fields, each before it closes the channel that publishes them. Every
// error from the five calls is a status fact, never a failure of init.

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/machine"
)

// serialLine is the syscall boundary of one attachment: the four calls
// made on an open tty. seriotty.go is the real one, and the tests
// supply a fake, because an attachment needs a serial line, serport,
// and CAP_SYS_ADMIN, and a test host has none of the three.
type serialLine interface {
	setTermios(t *unix.Termios) error
	setLineDiscipline(discipline int) error
	setSerioType(serioType uint64) error
	read() error
	close() error
}

// openLine opens a tty for an attachment.
type openLine func(path string) (serialLine, error)

// nMouse is the line discipline number that serport registers. The
// kernel's name for it is N_MOUSE, from the serial mice that serport
// was first written for.
const nMouse = 2

// termiosSpeed returns the termios speed bits for a table's baud rate.
// The table names 9600 only, because both USB-CEC adapters speak it.
// It is a switch and not a map, so a holder reads no map at all.
func termiosSpeed(baud int) (uint32, bool) {
	switch baud {
	case 9600:
		return unix.B9600, true
	}
	return 0, false
}

// serioTermios builds the line settings inputattach's setline writes:
// 8 data bits, the receiver on, a hangup on the last close, no modem
// control, no break and no parity errors in the input, no output or
// local processing, and a read that returns after one byte. The speed
// goes in both directions.
func serioTermios(baud int) (*unix.Termios, error) {
	speed, ok := termiosSpeed(baud)
	if !ok {
		return nil, fmt.Errorf("TCSETS: no termios speed for %d baud", baud)
	}
	t := &unix.Termios{
		Cflag:  unix.CS8 | unix.CREAD | unix.HUPCL | unix.CLOCAL | speed,
		Iflag:  unix.IGNBRK | unix.IGNPAR,
		Ispeed: speed,
		Ospeed: speed,
	}
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return t, nil
}

// attachSerio runs the first four calls: it opens the tty, sets the
// line, sets the discipline, and states the serio type. It returns the
// open line, ready for the read that creates the port. Each error
// names its call and carries the kernel's text word for word, and a
// line that fails after it opened is closed again.
func attachSerio(open openLine, path string, p machine.SerioProtocol) (serialLine, error) {
	line, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := setUpLine(line, p); err != nil {
		line.close()
		return nil, err
	}
	return line, nil
}

func setUpLine(line serialLine, p machine.SerioProtocol) error {
	t, err := serioTermios(p.Baud)
	if err != nil {
		return err
	}
	if err := line.setTermios(t); err != nil {
		return fmt.Errorf("TCSETS: %w", err)
	}
	if err := line.setLineDiscipline(nMouse); err != nil {
		return fmt.Errorf("TIOCSETD: %w", err)
	}
	// SPIOCSTYPE carries the type in the low byte, then an id byte and
	// an extra byte. The CEC adapters use neither, so both are zero.
	if err := line.setSerioType(uint64(p.Type)); err != nil {
		return fmt.Errorf("SPIOCSTYPE: %w", err)
	}
	return nil
}

// holdRead is the fifth call. serport's read blocks until the port
// ends and then returns zero bytes, so a nil return means the port is
// gone. inputattach repeats the read on EINTR and EAGAIN, and so does
// this loop. The loop allocates nothing of its own and compares errno
// values directly.
func holdRead(line serialLine) error {
	for {
		err := line.read()
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		return err
	}
}

// faultSignals are the signals the kernel raises on the thread that
// faults. The holder leaves them open. When a thread faults with its
// fault signal blocked, the kernel resets the signal to its default
// action, which ends the process, and for PID 1 that is a kernel
// panic. With the signal open, Go's handler turns a nil dereference
// into a panic that the holder's recover catches.
var faultSignals = []unix.Signal{
	unix.SIGSEGV, unix.SIGBUS, unix.SIGFPE, unix.SIGILL, unix.SIGTRAP, unix.SIGSYS,
}

// holderSignalMask is every signal except the fault signals. The mask
// includes the real-time signal that the Go runtime sends to every
// thread for a setuid-family call (runtime.doAllThreadsSyscall). Init
// runs as root for its whole life and makes no such call; if it ever
// did, a blocked holder thread would stall that call, where an open
// one would lose its port.
func holderSignalMask() unix.Sigset_t {
	var set unix.Sigset_t
	for i := range set.Val {
		set.Val[i] = ^set.Val[i]
	}
	for _, sig := range faultSignals {
		width := int(unsafe.Sizeof(set.Val[0])) * 8
		bit := int(sig) - 1
		set.Val[bit/width] &^= 1 << (bit % width)
	}
	return set
}

// sigsetHas reports whether a signal is in a set.
func sigsetHas(set *unix.Sigset_t, sig unix.Signal) bool {
	width := int(unsafe.Sizeof(set.Val[0])) * 8
	bit := int(sig) - 1
	return set.Val[bit/width]&(1<<(bit%width)) != 0
}

// serioHolder is one goroutine that holds one attachment. The walk
// that starts it reads its outcome through two channels. settled
// closes when the four attach calls finish, after attachErr is
// written, and a nil attachErr means the holder is in its read. done
// closes when the goroutine ends, after endErr is written.
type serioHolder struct {
	settled   chan struct{}
	attachErr error
	done      chan struct{}
	endErr    error
}

// startSerioHolder starts the goroutine that attaches the tty at path
// and holds the read. nudge is the walk's wake channel: the holder
// sends on it when it ends, so the walk reports the end without
// waiting for the next uevent.
func startSerioHolder(open openLine, path string, p machine.SerioProtocol, nudge chan<- struct{}) *serioHolder {
	h := &serioHolder{settled: make(chan struct{}), done: make(chan struct{})}
	go h.run(open, path, p, nudge)
	return h
}

func (h *serioHolder) run(open openLine, path string, p machine.SerioProtocol, nudge chan<- struct{}) {
	attached := false
	var line serialLine
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("the holder panicked: %v", r)
			if attached {
				h.endErr = err
				// A panic in the read leaves the tty open with serport's
				// discipline on it. Closing it lets a later holder open
				// the line again.
				closeAfterPanic(line)
			} else {
				h.attachErr = err
			}
		}
		if !attached {
			close(h.settled)
		}
		close(h.done)
		select {
		case nudge <- struct{}{}:
		default:
		}
	}()

	// The goroutine never unlocks its thread. When a goroutine ends
	// while it holds its lock, the Go runtime ends the thread with it,
	// so the blocked mask leaves with the thread and never reaches a
	// goroutine that expects signals.
	runtime.LockOSThread()
	mask := holderSignalMask()
	if err := unix.PthreadSigmask(unix.SIG_SETMASK, &mask, nil); err != nil {
		h.attachErr = fmt.Errorf("blocking signals on the holder's thread: %w", err)
		return
	}
	opened, err := attachSerio(open, path, p)
	if err != nil {
		h.attachErr = err
		return
	}
	line = opened
	close(h.settled)
	attached = true
	h.endErr = holdRead(line)
	line.close()
}

// closeAfterPanic closes a line inside the holder's recovery. A second
// panic there would escape the recover that is already running and
// end PID 1, so the close carries a recover of its own.
func closeAfterPanic(line serialLine) {
	defer func() { _ = recover() }()
	if line != nil {
		line.close()
	}
}
