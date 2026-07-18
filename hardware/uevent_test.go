package hardware

import "testing"

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
