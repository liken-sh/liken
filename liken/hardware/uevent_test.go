package hardware

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
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
			event, ok := parseUevent([]byte(tc.datagram))
			if got := ok && hardwareChanged(event); got != tc.want {
				t.Errorf("hardwareChanged(%q) = %v, want %v", tc.datagram, got, tc.want)
			}
		})
	}
}

// parseUevent reads where the event happened, so that a caller's
// match can drop the events under a path or a subsystem its walk does
// not read.
func TestParseUeventReadsTheDevicePathAndSubsystem(t *testing.T) {
	event, ok := parseUevent([]byte("add@/devices/virtual/net/veth1/queues/rx-0\x00ACTION=add\x00DEVPATH=/devices/virtual/net/veth1/queues/rx-0\x00SUBSYSTEM=queues\x00SEQNUM=4242"))
	want := Uevent{Action: "add", DevPath: "/devices/virtual/net/veth1/queues/rx-0", Subsystem: "queues"}
	if !ok || event != want {
		t.Errorf("parseUevent = %+v, %v, want %+v", event, ok, want)
	}
}

// These tests drive the reader without a real uevent socket. A test
// cannot make the kernel send a crafted uevent, but the reader only
// needs a non-blocking datagram descriptor to read from and a peer to
// write to. A socketpair gives
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
	go readUevents(reader, cancelR, nil, notify)

	unix.Write(peer, []byte("add@/devices/pci0000:00/usb1"))
	awaitSignal(t, notify)

	unix.Close(cancelW)
	unix.Close(peer)
}

// TestReadUeventsStopsOnADescriptorThatIsNotOpen proves the reader
// stops, and closes its channel, when poll reports that the socket is
// not an open descriptor. Nothing can repair that descriptor, so a
// reader that kept polling it would spin, and a reader that returned
// in silence would leave its caller deaf. The closed channel tells the
// caller to open the listener again. This test needs a descriptor
// number the process never opened, not merely one it closed: a
// just-closed number can be handed back out to something else in the
// runtime before the reader gets to it. A number far past anything
// this process could have allocated has no such race, and poll reports
// it with POLLNVAL. synctest.Wait returns once the reader returns,
// because a goroutine inside a system call is not durably blocked.
func TestReadUeventsStopsOnADescriptorThatIsNotOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const neverOpenedFd = 1 << 20
		cancelR, cancelW := cancelPipe(t)
		notify := make(chan struct{}, 1)
		go readUevents(neverOpenedFd, cancelR, nil, notify)

		synctest.Wait()

		if !isClosed(notify) {
			t.Error("the reader left its channel open on a descriptor that is not open")
		}
		unix.Close(cancelW)
	})
}

// erroredSocket answers a non-blocking UDP socket with an error queued
// on it. The socket is connected to a loopback port that nothing holds,
// so the datagram it sends draws an ICMP port unreachable, and the
// kernel queues ECONNREFUSED. poll then reports the socket ready, and
// the read answers the error instead of a datagram, which is how a
// netlink socket that overflowed delivers ENOBUFS.
func erroredSocket(t *testing.T) int {
	t.Helper()
	closed, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(closed, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	addr, err := unix.Getsockname(closed)
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(closed)
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Connect(fd, addr); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Write(fd, []byte("anyone there?")); err != nil {
		t.Fatal(err)
	}
	return fd
}

// TestReadUeventsWakesOnALostDatagram proves the reader wakes the
// channel, and keeps reading, when the read answers an error that is
// neither EAGAIN nor EINTR. The datagram is gone, so the reader wakes
// the sysfs walk instead of waiting for an unrelated later uevent.
func TestReadUeventsWakesOnALostDatagram(t *testing.T) {
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	go readUevents(erroredSocket(t), cancelR, nil, notify)

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
	go readUevents(reader, cancelR, nil, notify)

	unix.Write(peer, []byte("change@/devices/virtual/block/loop0"))
	refuteSignal(t, notify)

	unix.Close(cancelW)
	unix.Close(peer)
}

// TestReadUeventsDropsWhatTheMatchRefuses proves the caller's match
// filters the wakes. The match here refuses the virtual network
// devices, so the add of a veth pair wakes nothing, and the add of a
// USB device behind it still wakes the channel.
func TestReadUeventsDropsWhatTheMatchRefuses(t *testing.T) {
	reader, peer := ueventSocketpair(t)
	cancelR, cancelW := cancelPipe(t)
	notify := make(chan struct{}, 1)
	notVirtual := func(e Uevent) bool { return !strings.HasPrefix(e.DevPath, "/devices/virtual/") }
	go readUevents(reader, cancelR, notVirtual, notify)

	unix.Write(peer, []byte("add@/devices/virtual/net/veth1\x00SUBSYSTEM=net"))
	refuteSignal(t, notify)
	unix.Write(peer, []byte("add@/devices/pci0000:00/0000:00:03.0/usb1/1-2\x00SUBSYSTEM=usb"))
	awaitSignal(t, notify)

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
		readUevents(reader, cancelR, nil, notify)
		close(done)
	}()

	unix.Close(cancelW)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader did not exit after the cancel pipe closed")
	}
	select {
	case _, ok := <-notify:
		if !ok {
			t.Fatal("the reader closed its channel on a cancel, which tells a caller the listener failed")
		}
	default:
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
	notify, err := watchUevents(ctx, reader, nil)
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

// socketsAndPipes lists the sockets and pipes the process holds open,
// by the kernel's name for each one, such as "socket:[1234]". The name
// carries the inode, so a descriptor number that the runtime reuses for
// something else does not read as the same socket.
func socketsAndPipes(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]bool{}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && (strings.HasPrefix(target, "socket:") || strings.HasPrefix(target, "pipe:")) {
			held[target] = true
		}
	}
	return held
}

// opened lists what after holds that before did not.
func opened(before, after map[string]bool) []string {
	var names []string
	for name := range after {
		if !before[name] {
			names = append(names, name)
		}
	}
	return names
}

// isClosed reports whether a wake channel is closed. It drains a wake
// that is still pending first, because the channel holds one at most.
func isClosed(ch <-chan struct{}) bool {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return true
			}
		default:
			return false
		}
	}
}

// TestListenForUeventsReleasesItsDescriptorsOnCancel proves that the
// real listener opens the kernel's uevent socket and its cancel pipe,
// and that a cancel closes both without closing the channel. The kernel
// lets any process receive its uevent broadcasts, so the test needs no
// root. A component replaces its listener each time one stops, so a
// listener that kept its socket after a cancel would leak one socket
// for each restart. The closed channel is how a listener reports a
// failure, so a cancel must leave it open. synctest.Wait returns once
// the reader leaves its poll and returns, so a listener that ignored
// the cancel hangs the test instead of passing it.
func TestListenForUeventsReleasesItsDescriptorsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before := socketsAndPipes(t)
		ctx, cancel := context.WithCancel(t.Context())
		notify, err := ListenForUevents(ctx)
		if err != nil {
			t.Skipf("the uevent socket is not open to this user: %v", err)
		}
		held := opened(before, socketsAndPipes(t))

		cancel()
		synctest.Wait()

		after := socketsAndPipes(t)
		if len(held) != 2 || slices.ContainsFunc(held, func(name string) bool { return after[name] }) {
			t.Errorf("the listener opened %q and still holds some of them, want its socket and its cancel pipe, both released", held)
		}
		if isClosed(notify) {
			t.Error("the listener closed its channel on a cancel, which tells a caller the listener failed")
		}
	})
}
