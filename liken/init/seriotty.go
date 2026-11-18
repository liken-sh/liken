package main

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// ttyLine is the serialLine that makes the system calls on an open
// tty.
type ttyLine struct {
	fd int
}

// openTTY opens a tty with the flags that inputattach uses. O_NOCTTY
// stops the tty from becoming init's controlling terminal, and
// O_NONBLOCK lets the open return before the modem lines show that the
// device is ready. O_CLOEXEC is not one of inputattach's flags. It
// stops k3s, which init starts, from inheriting the descriptor and
// keeping the port open.
func openTTY(path string) (serialLine, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return ttyLine{fd: fd}, nil
}

// setTermios is tcsetattr with TCSANOW, which is the TCSETS ioctl.
func (l ttyLine) setTermios(t *unix.Termios) error {
	return unix.IoctlSetTermios(l.fd, unix.TCSETS, t)
}

// setLineDiscipline is the TIOCSETD ioctl. It takes the discipline
// number through a pointer to an int.
func (l ttyLine) setLineDiscipline(discipline int) error {
	return unix.IoctlSetPointerInt(l.fd, unix.TIOCSETD, discipline)
}

// spiocstype is SPIOCSTYPE, which the kernel's
// include/uapi/linux/serio.h defines as _IOW('q', 0x01, unsigned long)
// and x/sys/unix does not define. _IOW puts the write direction in
// bits 30 and 31, the argument size in bits 16 to 29, the type
// character in bits 8 to 15, and the command number in bits 0 to 7.
const spiocstype = 1<<30 | uintptr(unsafe.Sizeof(uintptr(0)))<<16 | uintptr('q')<<8 | 0x01

// setSerioType is the SPIOCSTYPE ioctl. serport reads the argument as
// an unsigned long through a pointer, so the value is passed in a
// uintptr.
func (l ttyLine) setSerioType(serioType uint64) error {
	value := uintptr(serioType)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(l.fd), spiocstype, uintptr(unsafe.Pointer(&value)))
	if errno != 0 {
		return errno
	}
	return nil
}

// read calls read(fd, NULL, 0), as inputattach does. serport
// registers the serio port in its read and blocks there until the port
// closes, and it ignores the buffer.
func (l ttyLine) read() error {
	_, err := unix.Read(l.fd, nil)
	return err
}

func (l ttyLine) close() error {
	return unix.Close(l.fd)
}
