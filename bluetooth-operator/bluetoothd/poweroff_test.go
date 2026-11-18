package main

// These tests run start-bluetoothd against a fake of the kernel's
// management channel, and check the state of each adapter at the
// moment of the exec.

import (
	"encoding/binary"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// fakeManagement replies to READ_INDEX_LIST and SET_POWERED as the
// kernel's net/bluetooth/mgmt.c does. Each Read returns one whole
// packet, as a read of the real socket does.
type fakeManagement struct {
	// powered is the power state of each adapter, by index.
	powered map[uint16]bool

	// refuse makes SET_POWERED for an index fail with this status, as
	// the kernel does for a socket opened without CAP_NET_ADMIN.
	refuse map[uint16]byte

	// announce sends a New Settings event before each reply. The
	// kernel sends it to every management socket when an adapter's
	// settings change, so a reader has to skip it.
	announce bool

	// silent sends no reply, so Read fails as the real socket's read
	// does when it times out.
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

// startWith runs the start against one fake kernel and returns the
// power state of each adapter at the exec, and the log.
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
		t.Fatalf("starting bluetoothd: %v", err)
	}
	if atExec == nil {
		t.Fatalf("bluetoothd did not start, log:\n%s", log.String())
	}
	return atExec, log.String()
}

// When powering an adapter off fails, bluetoothd still starts, with
// that adapter unchanged.
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
				t.Fatalf("at the exec the adapters were %v, want %v, log:\n%s", got, c.want, log)
			}
		})
	}
}

// Each failure logs the kernel's error, so the container's log shows
// why bluetoothd started on a powered adapter.
func TestTheLogRecordsEachPowerOffResult(t *testing.T) {
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
				"start-bluetoothd: powering hci0 off: the kernel replied with status Permission Denied (0x14)",
				"start-bluetoothd: powered hci1 off, so bluetoothd starts it with no connection",
			},
		},
		{
			name:   "no answer",
			kernel: silent,
			want:   []string{"start-bluetoothd: listing the adapters: the kernel sent no reply within 10s"},
		},
		{
			name:    "no channel",
			kernel:  newFakeManagement(map[uint16]bool{0: true}),
			openErr: errors.New("address family not supported by protocol"),
			want:    []string{"start-bluetoothd: opening the management channel: address family not supported by protocol"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, log := startWith(t, c.kernel, c.openErr)
			for _, line := range c.want {
				if !strings.Contains(log, line) {
					t.Errorf("the log does not contain %q, want it to:\n%s", line, log)
				}
			}
		})
	}
}

// The channel closes before the exec, so bluetoothd does not inherit
// this program's management socket.
func TestTheChannelClosesBeforeTheExec(t *testing.T) {
	kernel := newFakeManagement(map[uint16]bool{0: true})
	closedAtExec := false
	exec := func(string, []string, []string) error {
		closedAtExec = kernel.closed
		return nil
	}
	open := func() (io.ReadWriteCloser, error) { return kernel, nil }
	if err := becomeBluetoothd(open, io.Discard, exec); err != nil {
		t.Fatalf("starting bluetoothd: %v", err)
	}
	if !closedAtExec {
		t.Fatal("the management channel is open at the exec, want it closed")
	}
}
