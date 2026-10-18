package main

// start-bluetoothd powers every adapter off at the kernel before it
// becomes bluetoothd. These tests run it against a fake of the
// kernel's management channel, and check what the kernel holds at the
// moment of the exec.

import (
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// fakeManagement answers the two management commands the way the
// kernel's net/bluetooth/mgmt.c does, for the adapters it holds. Each
// Read returns one whole packet, as a read of the real socket does.
type fakeManagement struct {
	// powered holds each adapter by its index.
	powered map[uint16]bool

	// refuse answers SET_POWERED for an index with this status, the
	// way the kernel refuses a socket opened with no CAP_NET_ADMIN.
	refuse map[uint16]byte

	// announce sends the New Settings event before each reply. The
	// kernel sends it to every management socket when an adapter's
	// settings change, so a reader must skip it.
	announce bool

	// silent sends no reply, the way the real socket's read timeout
	// ends a wait for a kernel that does not answer.
	silent bool

	replies [][]byte
	closed  bool
}

func newFakeManagement(powered map[uint16]bool) *fakeManagement {
	return &fakeManagement{powered: powered, refuse: map[uint16]byte{}}
}

func (m *fakeManagement) Write(packet []byte) (int, error) {
	opcode := binary.LittleEndian.Uint16(packet[0:])
	index := binary.LittleEndian.Uint16(packet[2:])
	if m.silent {
		return len(packet), nil
	}
	switch opcode {
	case mgmt.OpReadIndexList:
		indexes := slices.Sorted(maps.Keys(m.powered))
		reply := binary.LittleEndian.AppendUint16(nil, uint16(len(indexes)))
		for _, each := range indexes {
			reply = binary.LittleEndian.AppendUint16(reply, each)
		}
		m.complete(opcode, index, 0, reply)
	case mgmt.OpSetPowered:
		if status, refused := m.refuse[index]; refused {
			m.send(mgmt.EvCmdStatus, index, binary.LittleEndian.AppendUint16(nil, opcode), status)
			return len(packet), nil
		}
		m.powered[index] = packet[mgmt.HeaderSize] == 1
		if m.announce {
			m.send(0x0006, index, []byte{0, 0, 0, 0})
		}
		m.complete(opcode, index, 0, []byte{0, 0, 0, 0})
	}
	return len(packet), nil
}

func (m *fakeManagement) complete(opcode, index uint16, status byte, reply []byte) {
	params := binary.LittleEndian.AppendUint16(nil, opcode)
	m.send(mgmt.EvCmdComplete, index, append(append(params, status), reply...))
}

func (m *fakeManagement) send(event, index uint16, params []byte, more ...byte) {
	params = append(params, more...)
	packet := binary.LittleEndian.AppendUint16(nil, event)
	packet = binary.LittleEndian.AppendUint16(packet, index)
	packet = binary.LittleEndian.AppendUint16(packet, uint16(len(params)))
	m.replies = append(m.replies, append(packet, params...))
}

func (m *fakeManagement) Read(buffer []byte) (int, error) {
	if len(m.replies) == 0 {
		return 0, errors.New("the kernel sent no reply within 10s")
	}
	packet := m.replies[0]
	m.replies = m.replies[1:]
	return copy(buffer, packet), nil
}

func (m *fakeManagement) Close() error {
	m.closed = true
	return nil
}

// startWith runs the start against one fake kernel, and returns what
// the kernel held when the exec ran, and the log.
func startWith(t *testing.T, kernel *fakeManagement, openErr error) (map[uint16]bool, string) {
	t.Helper()
	open := func() (io.ReadWriteCloser, error) { return kernel, openErr }
	var atExec map[uint16]bool
	exec := func(path string, argv, env []string) error {
		atExec = maps.Clone(kernel.powered)
		return nil
	}
	var log strings.Builder
	if err := becomeBluetoothd(open, &log, exec); err != nil {
		t.Fatal(err)
	}
	if atExec == nil {
		t.Fatalf("bluetoothd never started; log:\n%s", log.String())
	}
	return atExec, log.String()
}

// bluetoothd reads the kernel's connections only on an adapter that is
// already powered, so every adapter must be off when it starts. A
// failure to power one off still starts bluetoothd, on the adapter as
// it is.
func TestEveryAdapterIsOffWhenBluetoothdStarts(t *testing.T) {
	cases := []struct {
		name    string
		kernel  func() *fakeManagement
		openErr error
		want    map[uint16]bool
	}{
		{
			name:   "one powered adapter",
			kernel: func() *fakeManagement { return newFakeManagement(map[uint16]bool{0: true}) },
			want:   map[uint16]bool{0: false},
		},
		{
			name:   "two adapters, one already off",
			kernel: func() *fakeManagement { return newFakeManagement(map[uint16]bool{0: false, 3: true}) },
			want:   map[uint16]bool{0: false, 3: false},
		},
		{
			name: "the kernel announces each change before it replies",
			kernel: func() *fakeManagement {
				kernel := newFakeManagement(map[uint16]bool{0: true, 1: true})
				kernel.announce = true
				return kernel
			},
			want: map[uint16]bool{0: false, 1: false},
		},
		{
			name: "the kernel refuses one adapter",
			kernel: func() *fakeManagement {
				kernel := newFakeManagement(map[uint16]bool{0: true, 1: true})
				kernel.refuse[0] = 0x14
				return kernel
			},
			want: map[uint16]bool{0: true, 1: false},
		},
		{
			name: "the kernel does not answer",
			kernel: func() *fakeManagement {
				kernel := newFakeManagement(map[uint16]bool{0: true})
				kernel.silent = true
				return kernel
			},
			want: map[uint16]bool{0: true},
		},
		{
			name:    "the channel does not open",
			kernel:  func() *fakeManagement { return newFakeManagement(map[uint16]bool{0: true}) },
			openErr: errors.New("address family not supported by protocol"),
			want:    map[uint16]bool{0: true},
		},
		{
			name:   "no adapter",
			kernel: func() *fakeManagement { return newFakeManagement(map[uint16]bool{}) },
			want:   map[uint16]bool{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, log := startWith(t, c.kernel(), c.openErr)
			if !maps.Equal(got, c.want) {
				t.Fatalf("at the exec the adapters were %v, want %v; log:\n%s", got, c.want, log)
			}
		})
	}
}

// Each failure names the kernel's answer, so a person reads from the
// container's log why bluetoothd started on a powered adapter.
func TestTheLogNamesWhatThePowerOffDid(t *testing.T) {
	refusing := newFakeManagement(map[uint16]bool{0: true, 1: true})
	refusing.refuse[0] = 0x14
	silent := newFakeManagement(map[uint16]bool{0: true})
	silent.silent = true
	cases := []struct {
		name    string
		kernel  *fakeManagement
		openErr error
		want    []string
	}{
		{
			name:   "a refusal and a success",
			kernel: refusing,
			want: []string{
				"start-bluetoothd: powering hci0 off failed: the kernel answered Permission Denied (0x14)",
				"start-bluetoothd: powered hci1 off, so bluetoothd starts it with no connection",
			},
		},
		{
			name:   "no answer",
			kernel: silent,
			want:   []string{"start-bluetoothd: listing the adapters failed: the kernel sent no reply within 10s"},
		},
		{
			name:    "no channel",
			kernel:  newFakeManagement(map[uint16]bool{0: true}),
			openErr: errors.New("address family not supported by protocol"),
			want:    []string{"start-bluetoothd: address family not supported by protocol"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, log := startWith(t, c.kernel, c.openErr)
			for _, line := range c.want {
				if !strings.Contains(log, line) {
					t.Errorf("the log has no line %q:\n%s", line, log)
				}
			}
		})
	}
}

// The channel closes before the exec, so bluetoothd inherits no
// management socket of this program's.
func TestTheChannelClosesBeforeTheExec(t *testing.T) {
	kernel := newFakeManagement(map[uint16]bool{0: true})
	closedAtExec := false
	exec := func(string, []string, []string) error {
		closedAtExec = kernel.closed
		return nil
	}
	open := func() (io.ReadWriteCloser, error) { return kernel, nil }
	if err := becomeBluetoothd(open, io.Discard, exec); err != nil {
		t.Fatal(err)
	}
	if !closedAtExec {
		t.Fatal("the management channel was open at the exec")
	}
}
