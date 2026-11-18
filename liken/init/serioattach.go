package main

// This file attaches a serial line to the kernel's serio layer and
// keeps it attached. It makes the same system calls as the inputattach
// program, in the same order: open the tty, set raw mode at the
// device's baud rate, set serport's line discipline with TIOCSETD, set
// the serio type with SPIOCSTYPE, and read. serport registers the
// serio port when the read starts and unregisters it when the read
// returns (serport_ldisc_read in drivers/input/serio/serport.c). So the
// port, and every device that its driver creates, exists only while
// the read stays blocked.
//
// Init is PID 1 and reaps every orphaned process, so it receives
// SIGCHLD often. A signal delivered to the reading thread interrupts
// the read, and serport unregisters the port. Pods that hold the CEC
// adapter lose it, as if it were unplugged, and a new read registers a
// new port. So each holder locks its goroutine to one OS thread and
// blocks signals on that thread before its first call. The kernel
// delivers a signal sent to the process to a thread that does not
// block it, so the rest of init still receives SIGCHLD.
//
// The machine plane recovers a panic in a component's own goroutine,
// but not in a goroutine that the component starts. So the holder
// recovers its own panics, and the registry reports them in the
// entry's status. Neither a panic nor an error from a call stops init.

import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/liken/machine"
)

// serialLine is the system calls on one open tty, implemented in
// seriotty.go. The tests use a fake, because an attachment needs a
// serial line, serport, and CAP_SYS_ADMIN.
type serialLine interface {
	setTermios(t *unix.Termios) error
	setLineDiscipline(discipline int) error
	setSerioType(serioType uint64) error
	read() error
	close() error
}

type openLine func(path string) (serialLine, error)

// nMouse is the number of serport's line discipline. The kernel calls
// it N_MOUSE.
const nMouse = 2

// termiosSpeed supports only 9600 baud, the rate of the USB-CEC
// adapters. It is a switch and not a map so that a holder reads no
// map that another goroutine can write.
func termiosSpeed(baud int) (uint32, bool) {
	switch baud {
	case 9600:
		return unix.B9600, true
	}
	return 0, false
}

// serioTermios returns the line settings of inputattach's setline:
// raw mode, 8 data bits, no modem control, and a read that returns
// after one byte.
func serioTermios(baud int) (*unix.Termios, error) {
	speed, ok := termiosSpeed(baud)
	if !ok {
		return nil, fmt.Errorf("no termios speed for %d baud", baud)
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

// attachSerio makes every call but the read, and returns the open line
// ready for it. Each error names the failed step and its ioctl and
// keeps the kernel's error text, because the error becomes the entry's
// status message. When a call fails after the open, it closes the line.
func attachSerio(open openLine, path string, p machine.SerioProtocol) (serialLine, error) {
	line, err := open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
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
		return fmt.Errorf("setting the line speed and mode (TCSETS): %w", err)
	}
	if err := line.setLineDiscipline(nMouse); err != nil {
		return fmt.Errorf("setting the serport line discipline (TIOCSETD): %w", err)
	}
	// SPIOCSTYPE takes the type in the low byte, then an id byte and an
	// extra byte. The CEC adapters use neither, so both are zero.
	if err := line.setSerioType(uint64(p.Type)); err != nil {
		return fmt.Errorf("setting the serio type (SPIOCSTYPE): %w", err)
	}
	return nil
}

// holdRead makes the read that keeps the port registered. serport's
// read returns zero bytes when the port ends, so a nil return means the
// port is gone. Like inputattach, holdRead reads again after EINTR or
// EAGAIN.
func holdRead(line serialLine) error {
	for {
		err := line.read()
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		return err
	}
}

// faultSignals are the signals that the kernel sends to a thread that
// faults, and the holder leaves them unblocked. When a fault signal is
// blocked, the kernel resets it to its default action, which ends the
// process, and the kernel panics when PID 1 ends. When it is unblocked,
// Go's handler turns a nil dereference into a panic that the holder's
// recover catches.
var faultSignals = []unix.Signal{
	unix.SIGSEGV, unix.SIGBUS, unix.SIGFPE, unix.SIGILL, unix.SIGTRAP, unix.SIGSYS,
}

// holderSignalMask is every signal except the fault signals. It also
// blocks the real-time signal that the Go runtime sends to every thread
// for syscall.Setuid, Setgid, Setgroups, and the related calls
// (runtime.doAllThreadsSyscall). The runtime stops the world and waits
// until every thread handles that signal, and a holder's thread never
// does, so one of those calls would hang all of PID 1. Init runs as
// root, so it must never make them.
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

func sigsetHas(set *unix.Sigset_t, sig unix.Signal) bool {
	width := int(unsafe.Sizeof(set.Val[0])) * 8
	bit := int(sig) - 1
	return set.Val[bit/width]&(1<<(bit%width)) != 0
}

// serioHolder is one goroutine that keeps one serial line attached.
// The goroutine writes attachErr before it closes settled, and endErr
// before it closes done, so a reader that has seen the channel close
// can read the error without a lock. A nil attachErr means the attach
// succeeded and the holder went on to its read.
//
// The registry sets and reads the other fields under its own lock, and
// the goroutine never uses them.
type serioHolder struct {
	settled   chan struct{}
	attachErr error
	done      chan struct{}
	endErr    error

	// identity and usbPath come from the tty's serialLineInfo when the
	// holder started. failures counts the failures in a row on the USB
	// port before this holder (serioretry.go).
	identity uint64
	usbPath  string
	started  time.Time
	failures int
}

// startSerioHolder starts the goroutine that attaches the tty at path
// and makes the read. When the holder ends, it sends on nudge, so the
// registry runs a walk without waiting for a uevent.
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
			err := fmt.Errorf("the line holder panicked: %v", r)
			if attached {
				h.endErr = err
				// A panic in the read leaves the tty open with serport's
				// discipline set. Closing it lets a later holder open
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
	// while locked to a thread, the Go runtime ends the thread, or parks
	// it for good when it is the main thread. So no other goroutine runs
	// with the blocked signals.
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
	// The recover closes line when it is set, so clear it first, or a
	// panic in this close would close the line twice.
	last := line
	line = nil
	last.close()
}

// closeAfterPanic closes a line inside the holder's recover. It
// recovers its own panic, because a panic there would escape the
// holder's recover and end PID 1.
func closeAfterPanic(line serialLine) {
	defer func() { _ = recover() }()
	if line != nil {
		line.close()
	}
}
