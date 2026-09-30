package main

import (
	"encoding/binary"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

// testAddress is the adapter address of the machine this design was
// measured on.
var testAddress = bonds.Address{0x04, 0x4a, 0x69, 0x66, 0x92, 0x27}

// kernelDeviceInfo builds the struct hci_dev_info the kernel writes
// back into the ioctl's buffer: the index, the adapter's name, and the
// address in the kernel's own order, least significant octet first.
func kernelDeviceInfo(index uint16, name string, address bonds.Address) []byte {
	buffer := make([]byte, deviceInfoSize)
	binary.LittleEndian.PutUint16(buffer[devIDOffset:], index)
	copy(buffer[nameOffset:nameOffset+nameSize], name)
	for i, octet := range address {
		buffer[addressOffset+addressSize-1-i] = octet
	}
	return buffer
}

func TestParseDeviceInfoReadsTheKernelsLayout(t *testing.T) {
	adapter := parseDeviceInfo(kernelDeviceInfo(1, "hci1", testAddress))

	if adapter.Index != 1 {
		t.Errorf("index = %d", adapter.Index)
	}
	if adapter.Name != "hci1" {
		t.Errorf("name = %q", adapter.Name)
	}
	// The kernel holds the address least significant octet first, so a
	// reader that copied it straight through would read the address
	// 27:92:66:69:4A:04, the bytes in reverse, and name the
	// Secret after it.
	if got := adapter.Address.String(); got != "04:4A:69:66:92:27" {
		t.Errorf("address = %q", got)
	}
}

func TestParseDeviceInfoReadsTheAdapterThatIsNotReady(t *testing.T) {
	adapter := parseDeviceInfo(kernelDeviceInfo(0, "hci0", bonds.Address{}))

	if !adapter.Address.IsZero() {
		t.Errorf("address = %q, want the all-zero address", adapter.Address)
	}
}

// answers drives waitForAdapter without an adapter. Each call takes
// the next answer for that index, and the last answer repeats.
func answers(byIndex map[uint16][]deviceInfo) (adapterReader, *int) {
	calls := 0
	read := func(index uint16) (deviceInfo, error) {
		calls++
		queued, ok := byIndex[index]
		if !ok {
			// The kernel's answer for an index with no adapter behind
			// it.
			return deviceInfo{}, unix.ENODEV
		}
		answer := queued[0]
		if len(queued) > 1 {
			byIndex[index] = queued[1:]
		}
		return answer, nil
	}
	return read, &calls
}

func ready(index uint16, name string) deviceInfo {
	return parseDeviceInfo(kernelDeviceInfo(index, name, testAddress))
}

func notReady(index uint16, name string) deviceInfo {
	return parseDeviceInfo(kernelDeviceInfo(index, name, bonds.Address{}))
}

// The kernel registers an adapter before the queued power-on work has
// read the address out of the controller, so the first answers report
// the all-zero address. For a USB dongle that lasts about a second.
func TestWaitForAdapterWaitsForARealAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		read, _ := answers(map[uint16][]deviceInfo{
			0: {notReady(0, "hci0"), notReady(0, "hci0"), ready(0, "hci0")},
		})
		start := time.Now()

		adapter, err := waitForAdapter(read)
		if err != nil {
			t.Fatalf("waitForAdapter: %v", err)
		}
		if got := adapter.Address.String(); got != "04:4A:69:66:92:27" {
			t.Fatalf("address = %q", got)
		}
		// The third read is the first with an address, and it comes
		// two polls after the first.
		if waited := time.Since(start); waited != 2*adapterPoll {
			t.Errorf("the wait took %s, want %s", waited, 2*adapterPoll)
		}
	})
}

// An adapter that never reports an address blocks the pod. bluetoothd
// must not start with a tree this program could not fill. The wait
// gives up at its timeout.
func TestWaitForAdapterGivesUp(t *testing.T) {
	cases := []struct {
		name    string
		answers map[uint16][]deviceInfo
	}{
		{name: "no address arrives", answers: map[uint16][]deviceInfo{0: {notReady(0, "hci0")}}},
		{name: "no adapter at all", answers: map[uint16][]deviceInfo{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				read, _ := answers(c.answers)
				start := time.Now()

				if _, err := waitForAdapter(read); err == nil {
					t.Fatal("waitForAdapter answered with an adapter that has no address")
				}
				if waited := time.Since(start); waited != adapterTimeout {
					t.Errorf("the wait gave up after %s, want %s", waited, adapterTimeout)
				}
			})
		})
	}
}

// Indexes are not contiguous. An adapter that is unplugged and plugged
// again comes back at a higher index while the lower one stays absent,
// so the scan reads the whole range instead of stopping at the first
// gap.
func TestWaitForAdapterSkipsIndexesWithNoAdapter(t *testing.T) {
	read, _ := answers(map[uint16][]deviceInfo{3: {ready(3, "hci3")}})

	adapter, err := waitForAdapter(read)
	if err != nil {
		t.Fatalf("waitForAdapter: %v", err)
	}
	if adapter.Index != 3 || adapter.Name != "hci3" {
		t.Fatalf("adapter = %+v", adapter)
	}
}

// A node serves one adapter, so the lowest index that reports an
// address is the answer.
func TestWaitForAdapterTakesTheLowestReadyIndex(t *testing.T) {
	read, _ := answers(map[uint16][]deviceInfo{
		1: {ready(1, "hci1")},
		2: {ready(2, "hci2")},
	})

	adapter, err := waitForAdapter(read)
	if err != nil {
		t.Fatalf("waitForAdapter: %v", err)
	}
	if adapter.Index != 1 {
		t.Fatalf("index = %d, want 1", adapter.Index)
	}
}
