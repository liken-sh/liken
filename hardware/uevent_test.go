package hardware

import (
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRecvErrorLostAUevent proves the decision the reader loop makes
// about a Recvfrom error, without a real netlink socket. EAGAIN and
// EINTR leave nothing unread, so they report no loss. ENOBUFS is the
// case this decision exists for: the kernel dropped datagrams before
// the call ever ran. Every other error gets the same answer as ENOBUFS,
// because the reasoning is the same for any of them: poll reported the
// socket ready, and the call still returned no datagram. A wrapped
// ENOBUFS proves the check sees through fmt.Errorf's %w, the same way
// Go's os and net packages wrap a syscall error before it reaches a
// caller.
func TestRecvErrorLostAUevent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"EAGAIN", unix.EAGAIN, false},
		{"EINTR", unix.EINTR, false},
		{"ENOBUFS", unix.ENOBUFS, true},
		{"wrapped ENOBUFS", fmt.Errorf("recvfrom: %w", unix.ENOBUFS), true},
		{"an unrelated error", unix.EBADF, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recvErrorLostAUevent(tc.err); got != tc.want {
				t.Errorf("recvErrorLostAUevent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestHardwareChanged(t *testing.T) {
	cases := []struct {
		name     string
		datagram string
		want     bool
	}{
		{"add", "add@/devices/pci0000:00/0000:00:04.0/usb2/2-1\x00ACTION=add\x00MODALIAS=usb:v46F4p0001", true},
		{"remove", "remove@/devices/pci0000:00/0000:00:04.0/usb2/2-1", true},
		{"bind", "bind@/devices/pci0000:00/0000:00:04.0/usb2/2-1:1.0", true},
		{"unbind", "unbind@/devices/pci0000:00/0000:00:04.0/usb2/2-1:1.0", true},
		{"change", "change@/devices/virtual/block/loop0", false},
		{"not a uevent", "libudev\x00\x01\x02", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hardwareChanged([]byte(tc.datagram)); got != tc.want {
				t.Errorf("hardwareChanged(%q) = %v, want %v", tc.datagram, got, tc.want)
			}
		})
	}
}

// These tests drive the reader without root. A real uevent socket needs
// privileges, but the reader only needs a non-blocking datagram
// descriptor to read from and a peer to write to. A socketpair gives
// both, so a test can send a crafted uevent and watch the reader wake or
// stop. The reader owns the descriptors it reads from, so a test only
// closes the peer and the cancel pipe's write end.

// ueventSocketpair returns a non-blocking datagram socket that stands in
// for the uevent socket, and the peer that a test writes datagrams to.
// The reader closes the first descriptor when it exits, so a test closes
// only the peer.
func ueventSocketpair(t *testing.T) (reader, peer int) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(fds[0], true); err != nil {
		t.Fatal(err)
	}
	return fds[0], fds[1]
}

// cancelPipe returns the read and write ends of a non-blocking cancel
// pipe, the same shape watchUevents builds. The reader closes the read
// end when it exits, so a test closes only the write end, which is the
// close that stops the reader.
func cancelPipe(t *testing.T) (r, w int) {
	t.Helper()
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	return pipe[0], pipe[1]
}

// awaitSignal fails the test if no signal arrives within a generous
// limit. The limit is long for a test, because the machine that runs it
// may be under load, and a false failure is worse than a slow pass.
func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a signal")
	}
}

// refuteSignal fails the test if any signal arrives within a short
// window. It proves silence, so it does not wait long.
func refuteSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("got a signal, wanted silence")
	case <-time.After(500 * time.Millisecond):
	}
}

// drainSignal clears a pending signal so a later assertion starts from a
// known-empty channel. The channel holds one signal at most.
func drainSignal(ch <-chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

// TestReadUeventsSignalsOnChange proves an add datagram wakes the
// channel. The reader reads the datagram, hardwareChanged reports a
// change, and one signal lands.
func TestReadUeventsSignalsOnChange(t *testing.T) {
	reader, peer := ueventSocketpair(t)
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	go readUevents(reader, cancelR, notify)

	unix.Write(peer, []byte("add@/devices/pci0000:00/usb1"))
	awaitSignal(t, notify)

	unix.Close(cancelW)
	unix.Close(peer)
}

// TestReadUeventsWakesOnALostDatagram proves the reader wakes the
// channel when Recvfrom returns an error that is neither EAGAIN nor
// EINTR. This test needs a descriptor number the process never opened,
// not merely one it closed: a just-closed number can be handed back out
// to something else in the runtime before the reader gets to it, which
// would make the descriptor valid again by the time poll runs. A number
// far past anything this process could have allocated has no such race;
// poll reports it ready with POLLNVAL, and Recvfrom on it always fails
// with EBADF. The reader cannot tell this apart from the error that
// matters, ENOBUFS, so it takes the same path either way: the datagram
// is gone, and it wakes the sysfs walk instead of waiting for an
// unrelated later uevent to trigger it.
func TestReadUeventsWakesOnALostDatagram(t *testing.T) {
	const neverOpenedFd = 1 << 20
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	go readUevents(neverOpenedFd, cancelR, notify)

	awaitSignal(t, notify)

	unix.Close(cancelW)
}

// TestReadUeventsIgnoresUnchanged proves a change datagram wakes
// nothing. hardwareChanged reports no change, so the reader drops it and
// the channel stays quiet.
func TestReadUeventsIgnoresUnchanged(t *testing.T) {
	reader, peer := ueventSocketpair(t)
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	go readUevents(reader, cancelR, notify)

	unix.Write(peer, []byte("change@/devices/virtual/block/loop0"))
	refuteSignal(t, notify)

	unix.Close(cancelW)
	unix.Close(peer)
}

// TestReadUeventsExitsWhenCancelPipeCloses proves the reader stops the
// moment the cancel pipe hangs up. The close puts a hangup on the read
// end, the poll wakes at once, and the reader returns. A close on the
// datagram socket alone could not do this, because a reader blocked in a
// read on a descriptor does not wake when that descriptor closes.
func TestReadUeventsExitsWhenCancelPipeCloses(t *testing.T) {
	reader, peer := ueventSocketpair(t)
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		readUevents(reader, cancelR, notify)
		close(done)
	}()

	unix.Close(cancelW)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader did not exit after the cancel pipe closed")
	}
	unix.Close(peer)
}

// TestWatchUeventsStopsAfterCancel proves watchUevents wires the context
// to the reader. A datagram before the cancel produces a signal. After
// the cancel settles, a datagram produces none, because the reader has
// returned and nothing drains the socket.
func TestWatchUeventsStopsAfterCancel(t *testing.T) {
	reader, peer := ueventSocketpair(t)
	ctx, cancel := context.WithCancel(context.Background())
	notify, err := watchUevents(ctx, reader)
	if err != nil {
		t.Fatal(err)
	}

	unix.Write(peer, []byte("add@/devices/pci0000:00/usb1"))
	awaitSignal(t, notify)

	cancel()
	// The reader exits on the next scheduler turn. Allow a bounded grace
	// for it, then demand silence under a datagram that a live reader
	// would have reported.
	time.Sleep(100 * time.Millisecond)
	drainSignal(notify)
	unix.Write(peer, []byte("add@/devices/pci0000:00/usb2"))
	refuteSignal(t, notify)

	unix.Close(peer)
}
