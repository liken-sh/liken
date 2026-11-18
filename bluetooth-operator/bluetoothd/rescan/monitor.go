package main

// A socket bound to the HCI monitor channel receives a copy of every
// HCI packet that any adapter on the machine sends or receives, and of
// every Management API command and event. Each read returns one whole
// packet. Binding the socket needs CAP_NET_RAW (hci_sock_bind in
// net/bluetooth/hci_sock.c).

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

// The header size and the packet types of the monitor channel, from
// include/net/bluetooth/hci_mon.h. The header fields are little-endian.
const (
	monitorHeaderSize = 6

	// monitorIndexNone binds the socket to every adapter.
	monitorIndexNone = 0xffff

	monitorDelIndex    = 1
	monitorCommand     = 2
	monitorEvent       = 3
	monitorCloseIndex  = 9
	monitorCtrlCommand = 16
)

// packet is one packet of the monitor channel.
type packet struct {
	kind  uint16
	index uint16
	data  []byte
}

// parsePacket splits one raw read into a packet. ok is false when the
// read is shorter than the header or than the length the header gives.
func parsePacket(raw []byte) (packet, bool) {
	if len(raw) < monitorHeaderSize {
		return packet{}, false
	}
	length := int(binary.LittleEndian.Uint16(raw[4:]))
	if len(raw) < monitorHeaderSize+length {
		return packet{}, false
	}
	return packet{
		kind:  binary.LittleEndian.Uint16(raw),
		index: binary.LittleEndian.Uint16(raw[2:]),
		data:  raw[monitorHeaderSize : monitorHeaderSize+length],
	}, true
}

// readPackets reads monitor on its own goroutine. It sends each packet
// on the first channel, and the error that ends the reads on the
// second. The goroutine checks ctx only while it waits to send a
// packet, so to stop a read that is blocked, close monitor.
func readPackets(ctx context.Context, monitor io.Reader) (<-chan packet, <-chan error) {
	packets := make(chan packet, 64)
	failed := make(chan error, 1)
	go func() {
		// A raw socket truncates a packet that does not fit the buffer,
		// so the buffer fits the longest packet that the 16-bit length
		// in the header allows.
		buffer := make([]byte, monitorHeaderSize+0xffff)
		for {
			n, err := monitor.Read(buffer)
			if err != nil {
				failed <- err
				return
			}
			if p, ok := parsePacket(buffer[:n]); ok {
				p.data = append([]byte(nil), p.data...)
				select {
				case packets <- p:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return packets, failed
}

// monitorSocket is an HCI socket bound to the monitor channel.
type monitorSocket struct {
	fd int
}

// openMonitor opens a monitorSocket for all adapters.
func openMonitor() (io.ReadCloser, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return nil, fmt.Errorf("opening an HCI socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrHCI{Dev: monitorIndexNone, Channel: unix.HCI_CHANNEL_MONITOR}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("binding the HCI socket to the monitor channel: %w", err)
	}
	return &monitorSocket{fd: fd}, nil
}

func (s *monitorSocket) Read(buffer []byte) (int, error) {
	n, err := unix.Read(s.fd, buffer)
	if err != nil {
		return 0, fmt.Errorf("reading the monitor channel: %w", err)
	}
	return n, nil
}

func (s *monitorSocket) Close() error {
	return unix.Close(s.fd)
}
