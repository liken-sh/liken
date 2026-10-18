package main

// start-rescan reads the monitor channel and adds a device again when
// the kernel leaves its scan off after a failed connection. These
// tests play the packets that the kernel copies to the monitor
// channel, in the order and at the times the kernel sends them, and
// check the Add Device commands that reach a fake of the Management
// API, with the time each one arrived.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// monitorChannel is the monitor socket. Each Read returns one packet
// that the test sends, and Close ends the reads.
type monitorChannel struct {
	packets chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newMonitorChannel() *monitorChannel {
	return &monitorChannel{packets: make(chan []byte), closed: make(chan struct{})}
}

func (m *monitorChannel) Read(buffer []byte) (int, error) {
	select {
	case p := <-m.packets:
		return copy(buffer, p), nil
	case <-m.closed:
		return 0, errors.New("reading the monitor channel: bad file descriptor")
	}
}

func (m *monitorChannel) Close() error {
	m.once.Do(func() { close(m.closed) })
	return nil
}

// managementKernel answers Add Device the way net/bluetooth/mgmt.c
// does, and keeps each command it received with the time it arrived.
type managementKernel struct {
	start time.Time

	// refuse answers every Add Device with this status when it is not
	// zero.
	refuse uint8

	mu   sync.Mutex
	adds []string
}

func (k *managementKernel) open() (io.ReadWriteCloser, error) {
	return &managementSocket{kernel: k}, nil
}

func (k *managementKernel) received() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.adds)
}

type managementSocket struct {
	kernel  *managementKernel
	replies [][]byte
}

func (s *managementSocket) Write(packet []byte) (int, error) {
	opcode := binary.LittleEndian.Uint16(packet)
	index := binary.LittleEndian.Uint16(packet[2:])
	params := packet[mgmt.HeaderSize:]
	if opcode == mgmt.OpAddDevice {
		d := readDevice(params, params[6])
		s.kernel.mu.Lock()
		s.kernel.adds = append(s.kernel.adds, fmt.Sprintf("+%s hci%d %s type %d action %d",
			time.Since(s.kernel.start), index, d, d.kind, params[7]))
		s.kernel.mu.Unlock()
	}
	body := binary.LittleEndian.AppendUint16(nil, opcode)
	body = append(body, s.kernel.refuse)
	body = append(body, params[:7]...)
	reply := binary.LittleEndian.AppendUint16(nil, mgmt.EvCmdComplete)
	reply = binary.LittleEndian.AppendUint16(reply, index)
	reply = binary.LittleEndian.AppendUint16(reply, uint16(len(body)))
	s.replies = append(s.replies, append(reply, body...))
	return len(packet), nil
}

func (s *managementSocket) Read(buffer []byte) (int, error) {
	if len(s.replies) == 0 {
		return 0, errors.New("the kernel sent no reply within 10s")
	}
	p := s.replies[0]
	s.replies = s.replies[1:]
	return copy(buffer, p), nil
}

func (s *managementSocket) Close() error { return nil }

// The packets the kernel copies to the monitor channel. Each builder
// returns one whole monitor packet for hci0.

const remote = "58:AB:4B:24:91:47"

// kernelAddress writes an address in the kernel's order, least
// significant byte first.
func kernelAddress(text string) []byte {
	var out []byte
	parts := strings.Split(text, ":")
	for i := len(parts) - 1; i >= 0; i-- {
		var b byte
		fmt.Sscanf(parts[i], "%02X", &b)
		out = append(out, b)
	}
	return out
}

func monitorPacket(kind uint16, data ...byte) []byte {
	p := binary.LittleEndian.AppendUint16(nil, kind)
	p = binary.LittleEndian.AppendUint16(p, 0)
	p = binary.LittleEndian.AppendUint16(p, uint16(len(data)))
	return append(p, data...)
}

// managementCommand is a Management API command that a socket sent,
// behind the cookie that names the socket.
func managementCommand(opcode uint16, params ...byte) []byte {
	data := []byte{1, 0, 0, 0}
	data = binary.LittleEndian.AppendUint16(data, opcode)
	return monitorPacket(monitorCtrlCommand, append(data, params...)...)
}

func addDevice(address string, kind, action byte) []byte {
	return managementCommand(mgmt.OpAddDevice, append(kernelAddress(address), kind, action)...)
}

func removeDevice(address string, kind byte) []byte {
	return managementCommand(mgmt.OpRemoveDevice, append(kernelAddress(address), kind)...)
}

func hciCommand(opcode uint16, params ...byte) []byte {
	data := binary.LittleEndian.AppendUint16(nil, opcode)
	data = append(data, byte(len(params)))
	return monitorPacket(monitorCommand, append(data, params...)...)
}

func hciEvent(code byte, params ...byte) []byte {
	return monitorPacket(monitorEvent, append([]byte{code, byte(len(params))}, params...)...)
}

func setScanEnable(enable byte) []byte { return hciCommand(hciLESetScanEnable, enable, 0) }

func commandComplete(opcode uint16, status byte) []byte {
	params := []byte{1}
	params = binary.LittleEndian.AppendUint16(params, opcode)
	return hciEvent(hciEvCommandComplete, append(params, status)...)
}

func commandStatus(opcode uint16, status byte) []byte {
	params := []byte{status, 1}
	return hciEvent(hciEvCommandStatus, binary.LittleEndian.AppendUint16(params, opcode)...)
}

// createConnection is LE Create Connection to one peer address, with a
// filter policy of zero, the way hci_le_create_conn_sync sends it.
func createConnection(address string, peerType byte) []byte {
	params := []byte{0x60, 0, 0x60, 0, 0, peerType}
	params = append(params, kernelAddress(address)...)
	return hciCommand(hciLECreateConnection, append(params, 0, 0x18, 0, 0x28, 0, 0, 0, 0x2a, 0, 0, 0, 0, 0)...)
}

func extCreateConnection(address string, peerType byte) []byte {
	params := []byte{0, 0, peerType}
	params = append(params, kernelAddress(address)...)
	return hciCommand(hciLEExtCreateConnection, append(params, 0x01)...)
}

// acceptListConnection is LE Create Connection to whichever device on
// the accept list advertises first.
func acceptListConnection() []byte {
	params := []byte{0x60, 0, 0x60, 0, 1, 0}
	params = append(params, make([]byte, 6)...)
	return hciCommand(hciLECreateConnection, append(params, 0, 0x18, 0, 0x28, 0, 0, 0, 0x2a, 0, 0, 0, 0, 0)...)
}

func connectionComplete(subevent, status byte) []byte {
	return hciEvent(hciEvLEMeta, append([]byte{subevent, status}, make([]byte, 17)...)...)
}

// step is one packet, sent after a wait.
type step struct {
	after  time.Duration
	packet []byte
}

func at(after time.Duration, packets ...[]byte) []step {
	steps := []step{{after: after, packet: packets[0]}}
	for _, p := range packets[1:] {
		steps = append(steps, step{packet: p})
	}
	return steps
}

// registered is what bluetoothd sends when it registers an adapter
// with one bonded remote: it removes every device, adds the remote for
// auto connect, and the kernel starts the passive scan for it.
func registered(kind byte) []step {
	return at(0,
		removeDevice("00:00:00:00:00:00", 0),
		addDevice(remote, kind, mgmt.ActionAutoConnect),
		setScanEnable(1), commandComplete(hciLESetScanEnable, 0),
	)
}

// attemptStarts is the kernel stopping the scan and connecting to the
// remote after its advertisement.
func attemptStarts(after time.Duration) []step {
	return at(after,
		setScanEnable(0), commandComplete(hciLESetScanEnable, 0),
		createConnection(remote, 0), commandStatus(hciLECreateConnection, 0),
	)
}

// play runs start-rescan in a bubble, sends each step, waits a minute
// more, and returns what the kernel received and the log.
func play(t *testing.T, kernel *managementKernel, steps ...[]step) ([]string, string) {
	t.Helper()
	var adds []string
	var log strings.Builder
	synctest.Test(t, func(t *testing.T) {
		kernel.start = time.Now()
		monitor := newMonitorChannel()
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, func() (io.ReadCloser, error) { return monitor, nil }, kernel.open, &log)
		}()
		for _, group := range steps {
			for _, s := range group {
				time.Sleep(s.after)
				monitor.packets <- s.packet
			}
		}
		time.Sleep(time.Minute)
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("start-rescan ended with %v", err)
		}
		adds = kernel.received()
	})
	return adds, log.String()
}

// The kernel's scan stays off after each of these failures, and the
// remote is added again rescanSettle after the last packet of the
// failure.
func TestAFailedConnectionThatLeavesTheScanOffAddsTheDeviceAgain(t *testing.T) {
	cases := []struct {
		name  string
		steps [][]step
		want  []string
	}{
		{
			name: "the controller reports the connection failed to be established",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
			want: []string{"+1h0m6s hci0 58:AB:4B:24:91:47 type 1 action 2"},
		},
		{
			name: "the controller refuses LE Create Connection",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Hour, setScanEnable(0), commandComplete(hciLESetScanEnable, 0)),
				at(0, createConnection(remote, 0), commandStatus(hciLECreateConnection, 0x0c)),
			},
			want: []string{"+1h0m5s hci0 58:AB:4B:24:91:47 type 1 action 2"},
		},
		{
			name: "the kernel cancels the failed connection two seconds later",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEEnhancedConnectionComplete, 0x3e)),
				at(2*time.Second, hciCommand(hciLECreateConnectionCancel), commandComplete(hciLECreateConnectionCancel, 0x0c)),
			},
			want: []string{"+1h0m8s hci0 58:AB:4B:24:91:47 type 1 action 2"},
		},
		{
			name: "the attempt times out and the controller sends no final event",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(20*time.Second, hciCommand(hciLECreateConnectionCancel), commandComplete(hciLECreateConnectionCancel, 0)),
			},
			want: []string{"+1h0m25s hci0 58:AB:4B:24:91:47 type 1 action 2"},
		},
		{
			name: "an extended connection to a random address fails",
			steps: [][]step{
				registered(mgmt.AddressLERandom),
				at(time.Hour, extCreateConnection(remote, 1), commandStatus(hciLEExtCreateConnection, 0)),
				at(time.Second, connectionComplete(hciLEEnhancedConnectionCompleteV2, 0x3e)),
			},
			want: []string{"+1h0m6s hci0 58:AB:4B:24:91:47 type 2 action 2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, log := play(t, &managementKernel{}, c.steps...)
			if !slices.Equal(got, c.want) {
				t.Fatalf("the kernel received %q, want %q; log:\n%s", got, c.want, log)
			}
		})
	}
}

// The program adds a device only when the kernel's scan stays off and
// bluetoothd chose the device for auto connect.
func TestTheDeviceIsNotAddedAgain(t *testing.T) {
	cases := []struct {
		name  string
		steps [][]step
	}{
		{
			name: "the kernel scans again by itself",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
				at(time.Millisecond, setScanEnable(1), commandComplete(hciLESetScanEnable, 0)),
			},
		},
		{
			name: "the remote connects",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0)),
			},
		},
		{
			name: "bluetoothd never added the device for auto connect",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Hour, createConnection("11:22:33:44:55:66", 0), commandStatus(hciLECreateConnection, 0)),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
		{
			name: "bluetoothd removed the device",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Minute, removeDevice(remote, mgmt.AddressLEPublic)),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
		{
			name: "bluetoothd added the device with another action",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Minute, addDevice(remote, mgmt.AddressLEPublic, 0x01)),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
		{
			name: "bluetoothd registers the adapter again with no devices",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Minute, removeDevice("00:00:00:00:00:00", 0)),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
		{
			name: "the kernel starts a new attempt during the wait, and it connects",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
				at(time.Second, createConnection(remote, 0), commandStatus(hciLECreateConnection, 0)),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0)),
			},
		},
		{
			name: "the adapter powers off during the wait",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
				at(time.Second, monitorPacket(monitorCloseIndex)),
			},
		},
		{
			name: "the adapter is removed during the wait",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				attemptStarts(time.Hour),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
				at(time.Second, monitorPacket(monitorDelIndex)),
			},
		},
		{
			name: "an attempt to the accept list fails",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Hour, acceptListConnection(), commandStatus(hciLECreateConnection, 0)),
				at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
		{
			name: "a connection completes with no attempt, as a peripheral",
			steps: [][]step{
				registered(mgmt.AddressLEPublic),
				at(time.Hour, connectionComplete(hciLEConnectionComplete, 0x3e)),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, log := play(t, &managementKernel{}, c.steps...)
			if len(got) != 0 {
				t.Fatalf("the kernel received %q, want nothing; log:\n%s", got, log)
			}
		})
	}
}

// Each Add Device writes one line that names the device, the HCI
// status of the failed attempt, and the kernel's answer. A deadline
// that passes after an Add Device with no scan writes one more line,
// because the device then cannot reconnect.
func TestTheLogNamesEachRepair(t *testing.T) {
	failure := [][]step{
		registered(mgmt.AddressLEPublic),
		attemptStarts(time.Hour),
		at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
	}
	cases := []struct {
		name   string
		kernel *managementKernel
		steps  [][]step
		want   []string
	}{
		{
			name:   "the kernel scans after the Add Device",
			kernel: &managementKernel{},
			steps:  append(slices.Clone(failure), at(5*time.Second+time.Millisecond, setScanEnable(1), commandComplete(hciLESetScanEnable, 0))),
			want: []string{
				"start-rescan: reading the HCI monitor channel",
				"start-rescan: hci0: a connection to 58:AB:4B:24:91:47 failed with HCI status 0x3e and the kernel did not scan again within 5s; added the device again, so the kernel scans for it",
			},
		},
		{
			name:   "the kernel does not scan after the Add Device",
			kernel: &managementKernel{},
			steps:  failure,
			want: []string{
				"start-rescan: reading the HCI monitor channel",
				"start-rescan: hci0: a connection to 58:AB:4B:24:91:47 failed with HCI status 0x3e and the kernel did not scan again within 5s; added the device again, so the kernel scans for it",
				"start-rescan: hci0: the kernel did not scan within 5s after 58:AB:4B:24:91:47 was added again; the device cannot reconnect until the adapter powers off and on",
			},
		},
		{
			name:   "the kernel refuses the Add Device",
			kernel: &managementKernel{refuse: 0x0a},
			steps:  failure,
			want: []string{
				"start-rescan: reading the HCI monitor channel",
				"start-rescan: hci0: a connection to 58:AB:4B:24:91:47 failed with HCI status 0x3e and the kernel did not scan again within 5s; adding the device again failed: the kernel answered Busy (0x0a)",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, log := play(t, c.kernel, c.steps...)
			got := strings.Split(strings.TrimSpace(log), "\n")
			if !slices.Equal(got, c.want) {
				t.Fatalf("the log is\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// A failure to open the management channel is a line in the log, and
// the program keeps reading.
func TestAManagementChannelThatDoesNotOpenIsLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		monitor := newMonitorChannel()
		var log strings.Builder
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		management := func() (io.ReadWriteCloser, error) {
			return nil, errors.New("binding the HCI socket to the management channel: operation not permitted")
		}
		go func() {
			done <- run(ctx, func() (io.ReadCloser, error) { return monitor, nil }, management, &log)
		}()
		for _, group := range [][]step{
			registered(mgmt.AddressLEPublic),
			attemptStarts(time.Hour),
			at(time.Second, connectionComplete(hciLEConnectionComplete, 0x3e)),
		} {
			for _, s := range group {
				time.Sleep(s.after)
				monitor.packets <- s.packet
			}
		}
		time.Sleep(time.Minute)
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := "adding the device again failed: binding the HCI socket to the management channel: operation not permitted"
		if !strings.Contains(log.String(), want) {
			t.Fatalf("the log has no %q:\n%s", want, log.String())
		}
	})
}

// The program ends with the monitor channel's error, so the kubelet
// restarts the container, and with the open's error when it never
// reads at all.
func TestTheProgramEndsWhenTheMonitorChannelFails(t *testing.T) {
	cases := []struct {
		name string
		open func() (io.ReadCloser, error)
		want string
	}{
		{
			name: "the channel does not open",
			open: func() (io.ReadCloser, error) {
				return nil, errors.New("binding the HCI socket to the monitor channel: operation not permitted")
			},
			want: "binding the HCI socket to the monitor channel: operation not permitted",
		},
		{
			name: "a read fails",
			open: func() (io.ReadCloser, error) {
				m := newMonitorChannel()
				m.Close()
				return m, nil
			},
			want: "reading the monitor channel: bad file descriptor",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				err := run(t.Context(), c.open, (&managementKernel{}).open, io.Discard)
				if err == nil || err.Error() != c.want {
					t.Fatalf("start-rescan ended with %v, want %q", err, c.want)
				}
			})
		})
	}
}
