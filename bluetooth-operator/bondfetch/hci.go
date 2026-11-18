package main

// bondfetch runs before bluetoothd, so it cannot read the adapter's
// address over D-Bus. It reads it from the kernel with the HCIGETDEVINFO
// ioctl on an HCI socket. The ioctl needs no capability, so the container
// can run as a non-root user with every capability dropped. The kernel
// creates Bluetooth sockets only in the host's network namespace, so the
// pod must use it.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

const (
	// btprotoHCI is BTPROTO_HCI. The socket is never bound and carries
	// no traffic. The ioctl only needs a file descriptor of this protocol.
	btprotoHCI = 1

	// hciGetDevInfo is HCIGETDEVINFO. The kernel header declares it
	// with an int argument, so the request number encodes four bytes,
	// but the kernel copies a whole struct hci_dev_info through the
	// pointer. The buffer must be at least that structure's size.
	hciGetDevInfo = 0x800448d3

	// The fields of struct hci_dev_info that this program reads. The
	// kernel reads dev_id from the buffer to choose the adapter, and
	// writes the rest of the structure.
	deviceInfoSize = 96
	devIDOffset    = 0
	nameOffset     = 2
	nameSize       = 8
	addressOffset  = 10
	addressSize    = 6

	// maxAdapters is the kernel's HCI_MAX_DEV, the number of adapter
	// indexes the kernel can register.
	maxAdapters = 16
)

type deviceInfo struct {
	Index   uint16
	Name    string
	Address bonds.Address
}

// adapterReader reads one adapter by index. waitForAdapter takes it as
// a parameter so that a test can run without Bluetooth hardware.
type adapterReader func(index uint16) (deviceInfo, error)

type hciSocket struct {
	fd int
}

func openHCISocket() (*hciSocket, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, btprotoHCI)
	if err != nil {
		return nil, fmt.Errorf("opening an HCI socket: %w", err)
	}
	return &hciSocket{fd: fd}, nil
}

// deviceInfo reads the adapter at one index from the kernel. An index
// with no adapter fails with ENODEV.
func (s *hciSocket) deviceInfo(index uint16) (deviceInfo, error) {
	var buffer [deviceInfoSize]byte
	binary.LittleEndian.PutUint16(buffer[devIDOffset:], index)
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(s.fd),
		uintptr(hciGetDevInfo),
		uintptr(unsafe.Pointer(&buffer[0])),
	)
	if errno != 0 {
		return deviceInfo{}, errno
	}
	return parseDeviceInfo(buffer[:]), nil
}

func (s *hciSocket) Close() error {
	return unix.Close(s.fd)
}

// parseDeviceInfo decodes the buffer that the ioctl filled. The kernel
// stores the address least significant octet first, and the text form
// of an address starts with the most significant octet, so this
// reverses the octets.
func parseDeviceInfo(buffer []byte) deviceInfo {
	var address bonds.Address
	for i := range address {
		address[i] = buffer[addressOffset+addressSize-1-i]
	}
	name := bytes.TrimRight(buffer[nameOffset:nameOffset+nameSize], "\x00")
	return deviceInfo{
		Index:   binary.LittleEndian.Uint16(buffer[devIDOffset:]),
		Name:    string(name),
		Address: address,
	}
}

// waitForAdapter polls until the kernel reports an adapter with a
// non-zero address, and fails after adapterTimeout. At a cold boot the
// kernel registers an adapter before its power-on work has read the
// address from the controller, and until then it reports
// 00:00:00:00:00:00.
//
// A failure exits the init container, so bluetoothd does not start
// without its bonds, and the kubelet restarts the container.
func waitForAdapter(read adapterReader) (deviceInfo, error) {
	deadline := time.Now().Add(adapterTimeout)
	for {
		if adapter, ok := readyAdapter(read); ok {
			return adapter, nil
		}
		if !time.Now().Before(deadline) {
			return deviceInfo{}, fmt.Errorf("no Bluetooth adapter reported an address within %s", adapterTimeout)
		}
		time.Sleep(adapterPoll)
	}
}

// readyAdapter returns the adapter with the lowest index that reports a
// non-zero address, and skips an index that fails. It reads every index
// below maxAdapters, because an adapter that is unplugged and plugged in
// again gets a higher index and leaves the lower one empty.
//
// The lowest index is the right adapter only on a node with one adapter.
// On a node with several, this does not select the USB device that the
// pod's claim allocated.
func readyAdapter(read adapterReader) (deviceInfo, bool) {
	for index := uint16(0); index < maxAdapters; index++ {
		adapter, err := read(index)
		if err != nil || adapter.Address.IsZero() {
			continue
		}
		return adapter, true
	}
	return deviceInfo{}, false
}
