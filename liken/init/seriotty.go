package main

// The real serial line behind serialLine: an open tty and the system
// calls serioattach.go makes on it.

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// ttyLine is one open tty.
type ttyLine struct {
	fd int
}

// openTTY opens a tty the way inputattach does. O_NOCTTY keeps the
// line from becoming init's controlling terminal, and O_NONBLOCK lets
// the open return before the modem lines say the device is ready.
// O_CLOEXEC is liken's addition: init starts k3s, and the child must
// not inherit a descriptor that holds the port open.
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

// setLineDiscipline is TIOCSETD. The kernel takes the discipline
// number through a pointer to an int.
func (l ttyLine) setLineDiscipline(discipline int) error {
	return unix.IoctlSetPointerInt(l.fd, unix.TIOCSETD, discipline)
}

// spiocstype is SPIOCSTYPE from the kernel's include/uapi/linux/serio.h,
// _IOW('q', 0x01, unsigned long): the write direction in bits 30 and
// 31, the argument's size in bits 16 to 29, the type character in bits
// 8 to 15, and the number in bits 0 to 7. x/sys/unix does not carry it.
const spiocstype = 1<<30 | uintptr(unsafe.Sizeof(uintptr(0)))<<16 | uintptr('q')<<8 | 0x01

// setSerioType is SPIOCSTYPE. serport reads the argument as an
// unsigned long through a pointer, so the value goes in a word of that
// size.
func (l ttyLine) setSerioType(serioType uint64) error {
	value := uintptr(serioType)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(l.fd), spiocstype, uintptr(unsafe.Pointer(&value)))
	if errno != 0 {
		return errno
	}
	return nil
}

// read is read(fd, NULL, 0), which inputattach calls. serport's read
// ignores the buffer, and a zero-length slice passes none.
func (l ttyLine) read() error {
	_, err := unix.Read(l.fd, nil)
	return err
}

func (l ttyLine) close() error {
	return unix.Close(l.fd)
}
