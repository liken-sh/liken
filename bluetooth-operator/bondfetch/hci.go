package main

// Reading an adapter's own address out of the kernel.
//
// bluetoothd is not running when this program runs, so there is no
// D-Bus to ask and no BlueZ storage tree to read the address from. The
// kernel answers instead: HCIGETDEVINFO on an HCI socket returns one
// adapter's index, name, and address.
//
// The call needs no privilege beyond the pod's own network namespace
// being the host's. It was measured working as uid 65534 with every
// capability dropped, a read-only root filesystem, and
// no-new-privileges.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// btprotoHCI is the protocol number of the HCI control channel
	// inside AF_BLUETOOTH. The socket carries no traffic here. It
	// exists because the ioctl needs a file descriptor of this family
	// to answer about adapters, and it is never bound to one.
	btprotoHCI = 1

	// hciGetDevInfo is HCIGETDEVINFO. The number encodes a four-byte
	// argument, because the kernel's header declares it with int, and
	// the kernel copies a whole struct hci_dev_info through the pointer
	// regardless. The buffer must be the size of that structure and not
	// four bytes.
	hciGetDevInfo = 0x800448d3

	// The layout of struct hci_dev_info, measured on a running machine.
	// The kernel reads dev_id out of the buffer to choose which adapter
	// to answer about, and writes the rest.
	//
	// The structure continues past the address with flags, the
	// adapter's type, its features, and its packet statistics. Nothing
	// here reads them: the address is both the identity this program
	// needs and the test of whether the adapter is ready.
	deviceInfoSize = 96
	devIDOffset    = 0
	nameOffset     = 2
	nameSize       = 8
	addressOffset  = 10
	addressSize    = 6

	// maxAdapters is the kernel's HCI_MAX_DEV, which is the highest
	// number of adapters one machine can register.
	maxAdapters = 16
)

// deviceInfo is what the kernel says about one adapter.
type deviceInfo struct {
	Index   uint16
	Name    string
	Address bonds.Address
}

// adapterReader answers about one adapter by index. The wait below
// takes one as a value, so a test drives it with no Bluetooth
// hardware and no privilege.
type adapterReader func(index uint16) (deviceInfo, error)

// hciSocket is the descriptor the ioctl runs on. One socket serves
// every read, because the scan below asks about sixteen indexes and
// may ask again a hundred times while an adapter powers on.
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

// deviceInfo asks the kernel about the adapter at one index. An index
// with no adapter behind it answers ENODEV, which the scan reads as
// "not this one" rather than as a failure.
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

// parseDeviceInfo reads the fields this program uses out of the
// kernel's answer. It is separate from the ioctl so that the layout is
// testable without an adapter.
//
// The kernel holds the address least significant octet first, and a
// person reads it the other way, so the octets are reversed here. A
// reader that copied them straight through would build the wrong
// address and name the Secret after it.
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

// waitForAdapter returns the adapter this pod's bonds belong to, once
// the kernel reports a real address for it.
//
// The retry makes this correct on a cold boot. hci_register_dev
// returns before the queued power-on work has read
// the address out of the controller, and until that work runs the
// kernel answers with 00:00:00:00:00:00. For a USB dongle the gap is
// about a second after enumeration. That address is not a real
// adapter, so the wait counts it as "not ready" and never as an answer.
//
// The wait is bounded, and running out is a failure the caller
// reports. An adapter that never reports an address is a pod that must
// not start bluetoothd, and the kubelet's restart is the retry.
func waitForAdapter(read adapterReader, timeout, poll time.Duration) (deviceInfo, error) {
	deadline := time.Now().Add(timeout)
	for {
		if adapter, ok := readyAdapter(read); ok {
			return adapter, nil
		}
		if !time.Now().Before(deadline) {
			return deviceInfo{}, fmt.Errorf("no Bluetooth adapter reported an address within %s", timeout)
		}
		time.Sleep(poll)
	}
}

// readyAdapter returns the lowest-numbered adapter that reports a real
// address.
//
// It reads the whole index range rather than stopping at the first
// gap, because the indexes are not contiguous: an adapter that is
// unplugged and plugged again comes back at a higher index while the
// lower one stays absent.
//
// The lowest index is the right answer because a node serves one
// adapter. The multi-adapter case is a mapping that belongs here and
// is not written: the pod's DRA claim delivers one USB device, and
// /sys/class/bluetooth/hciN resolves to a real path whose first
// ancestor holding an idVendor file is the USB device behind that
// adapter. Comparing that device against the claim would pick the
// claimed adapter instead of the lowest one.
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
