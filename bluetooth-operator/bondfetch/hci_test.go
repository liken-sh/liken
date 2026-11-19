package main

import (
	"encoding/binary"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
)

var testAddress = bonds.Address{0x04, 0x4a, 0x69, 0x66, 0x92, 0x27}

// kernelDeviceInfo builds the buffer that the ioctl fills: a struct
// hci_dev_info with the index, the name, and the address in the
// kernel's byte order, least significant octet first.
func kernelDeviceInfo(index uint16, name string, address bonds.Address) []byte {
	buffer := make([]byte, deviceInfoSize)
	binary.LittleEndian.PutUint16(buffer[devIDOffset:], index)
	copy(buffer[nameOffset:nameOffset+nameSize], name)
	for i, octet := range address {
		buffer[addressOffset+addressSize-1-i] = octet
	}
	return buffer
}

func TestParseDeviceInfoReversesTheAddressOctets(t *testing.T) {
	adapter := parseDeviceInfo(kernelDeviceInfo(1, "hci1", testAddress))

	if adapter.Index != 1 {
		t.Errorf("index is %d, want 1", adapter.Index)
	}
	if adapter.Name != "hci1" {
		t.Errorf("name is %q, want hci1", adapter.Name)
	}
	// Without the reversal the address would be 27:92:66:69:4A:04.
	if got := adapter.Address.String(); got != "04:4A:69:66:92:27" {
		t.Errorf("address is %q, want 04:4A:69:66:92:27", got)
	}
}

func TestParseDeviceInfoReadsAnAllZeroAddress(t *testing.T) {
	adapter := parseDeviceInfo(kernelDeviceInfo(0, "hci0", bonds.Address{}))

	if !adapter.Address.IsZero() {
		t.Errorf("address is %q, want the all-zero address", adapter.Address)
	}
}

// fakeReader returns an adapterReader that returns the listed results
// for each index in order and then repeats the last one. An index with
// no list fails with ENODEV, as the kernel does for an index with no
// adapter.
func fakeReader(byIndex map[uint16][]deviceInfo) adapterReader {
	return func(index uint16) (deviceInfo, error) {
		queued, ok := byIndex[index]
		if !ok {
			return deviceInfo{}, unix.ENODEV
		}
		result := queued[0]
		if len(queued) > 1 {
			byIndex[index] = queued[1:]
		}
		return result, nil
	}
}

func ready(index uint16, name string) deviceInfo {
	return parseDeviceInfo(kernelDeviceInfo(index, name, testAddress))
}

func notReady(index uint16, name string) deviceInfo {
	return parseDeviceInfo(kernelDeviceInfo(index, name, bonds.Address{}))
}

func TestWaitForAdapterWaitsForARealAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		read := fakeReader(map[uint16][]deviceInfo{
			0: {notReady(0, "hci0"), notReady(0, "hci0"), ready(0, "hci0")},
		})
		start := time.Now()

		adapter, err := waitForAdapter(read)
		if err != nil {
			t.Fatalf("waiting for an adapter: %v", err)
		}
		if got := adapter.Address.String(); got != "04:4A:69:66:92:27" {
			t.Fatalf("address is %q, want 04:4A:69:66:92:27", got)
		}
		// The third read is two poll intervals after the first.
		if waited := time.Since(start); waited != 2*adapterPoll {
			t.Errorf("the wait took %s, want %s", waited, 2*adapterPoll)
		}
	})
}

func TestWaitForAdapterGivesUp(t *testing.T) {
	cases := []struct {
		name    string
		results map[uint16][]deviceInfo
	}{
		{name: "no address arrives", results: map[uint16][]deviceInfo{0: {notReady(0, "hci0")}}},
		{name: "no adapter at all", results: map[uint16][]deviceInfo{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				read := fakeReader(c.results)
				start := time.Now()

				if _, err := waitForAdapter(read); err == nil {
					t.Fatal("waitForAdapter returned an adapter that has no address")
				}
				if waited := time.Since(start); waited != adapterTimeout {
					t.Errorf("the wait gave up after %s, want %s", waited, adapterTimeout)
				}
			})
		})
	}
}

// The kernel answers HCIGETDEVINFO with ENODEV for an index that has no
// adapter. Any other error, such as EFAULT for a request buffer that is
// wrong, is the same on every read, so the wait ends at once and names
// it.
func TestWaitForAdapterStopsAtAnErrorOtherThanNoDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		read := func(uint16) (deviceInfo, error) { return deviceInfo{}, unix.EFAULT }
		start := time.Now()

		_, err := waitForAdapter(read)
		if !errors.Is(err, unix.EFAULT) {
			t.Errorf("waitForAdapter returned %v, want the kernel's EFAULT", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("the wait took %s, want no wait because the error does not clear", waited)
		}
	})
}

func TestWaitForAdapterSkipsIndexesWithNoAdapter(t *testing.T) {
	read := fakeReader(map[uint16][]deviceInfo{3: {ready(3, "hci3")}})

	adapter, err := waitForAdapter(read)
	if err != nil {
		t.Fatalf("waiting for an adapter: %v", err)
	}
	if adapter.Index != 3 || adapter.Name != "hci3" {
		t.Fatalf("adapter is %+v, want hci3 at index 3", adapter)
	}
}

func TestWaitForAdapterTakesTheLowestReadyIndex(t *testing.T) {
	read := fakeReader(map[uint16][]deviceInfo{
		1: {ready(1, "hci1")},
		2: {ready(2, "hci2")},
	})

	adapter, err := waitForAdapter(read)
	if err != nil {
		t.Fatalf("waiting for an adapter: %v", err)
	}
	if adapter.Index != 1 {
		t.Fatalf("index is %d, want 1", adapter.Index)
	}
}
