package cec_test

// The layouts and the request numbers must match linux/cec.h exactly,
// because the kernel copies the argument of each ioctl byte for byte.
// The wanted values were compiled from /usr/include/linux/cec.h with
// sizeof, offsetof, and the _IOR/_IOW/_IOWR macros, so a Go field out
// of place fails here and not on the wire.

import (
	"testing"
	"unsafe"

	"github.com/liken-sh/equipment-operator/cec"
)

func TestTheKernelLayoutsMatchTheHeader(t *testing.T) {
	var msg cec.KernelMsg
	var addrs cec.KernelLogAddrs
	var event cec.KernelEvent
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"sizeof cec_msg", unsafe.Sizeof(msg), 56},
		{"offsetof cec_msg.len", unsafe.Offsetof(msg.Len), 16},
		{"offsetof cec_msg.timeout", unsafe.Offsetof(msg.Timeout), 20},
		{"offsetof cec_msg.sequence", unsafe.Offsetof(msg.Sequence), 24},
		{"offsetof cec_msg.flags", unsafe.Offsetof(msg.Flags), 28},
		{"offsetof cec_msg.msg", unsafe.Offsetof(msg.Msg), 32},
		{"offsetof cec_msg.reply", unsafe.Offsetof(msg.Reply), 48},
		{"offsetof cec_msg.rx_status", unsafe.Offsetof(msg.RxStatus), 49},
		{"offsetof cec_msg.tx_status", unsafe.Offsetof(msg.TxStatus), 50},
		{"offsetof cec_msg.tx_error_cnt", unsafe.Offsetof(msg.TxErrorCnt), 54},
		{"sizeof cec_caps", unsafe.Sizeof(cec.KernelCaps{}), 76},
		{"sizeof cec_log_addrs", unsafe.Sizeof(addrs), 92},
		{"offsetof cec_log_addrs.log_addr_mask", unsafe.Offsetof(addrs.LogAddrMask), 4},
		{"offsetof cec_log_addrs.vendor_id", unsafe.Offsetof(addrs.VendorID), 8},
		{"offsetof cec_log_addrs.flags", unsafe.Offsetof(addrs.Flags), 12},
		{"offsetof cec_log_addrs.osd_name", unsafe.Offsetof(addrs.OSDName), 16},
		{"offsetof cec_log_addrs.primary_device_type", unsafe.Offsetof(addrs.PrimaryDeviceType), 31},
		{"offsetof cec_log_addrs.log_addr_type", unsafe.Offsetof(addrs.LogAddrType), 35},
		{"offsetof cec_log_addrs.all_device_types", unsafe.Offsetof(addrs.AllDeviceTypes), 39},
		{"offsetof cec_log_addrs.features", unsafe.Offsetof(addrs.Features), 43},
		{"sizeof cec_event", unsafe.Sizeof(event), 80},
		{"offsetof cec_event.event", unsafe.Offsetof(event.Event), 8},
		{"offsetof cec_event.flags", unsafe.Offsetof(event.Flags), 12},
		{"offsetof cec_event.state_change", unsafe.Offsetof(event.Raw), 16},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %d, want %d", c.got, c.want)
			}
		})
	}
}

func TestTheRequestNumbersMatchTheHeader(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"CEC_ADAP_G_CAPS", cec.RequestGetCaps, 0xc04c6100},
		{"CEC_ADAP_G_PHYS_ADDR", cec.RequestGetPhysAddr, 0x80026101},
		{"CEC_ADAP_S_PHYS_ADDR", cec.RequestSetPhysAddr, 0x40026102},
		{"CEC_ADAP_G_LOG_ADDRS", cec.RequestGetLogAddrs, 0x805c6103},
		{"CEC_ADAP_S_LOG_ADDRS", cec.RequestSetLogAddrs, 0xc05c6104},
		{"CEC_TRANSMIT", cec.RequestTransmit, 0xc0386105},
		{"CEC_RECEIVE", cec.RequestReceive, 0xc0386106},
		{"CEC_DQEVENT", cec.RequestDequeueEvent, 0xc0506107},
		{"CEC_G_MODE", cec.RequestGetMode, 0x80046108},
		{"CEC_S_MODE", cec.RequestSetMode, 0x40046109},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %#x, want %#x", c.got, c.want)
			}
		})
	}
}
