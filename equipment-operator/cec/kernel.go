package cec

// The types and constants of the kernel's CEC API from linux/cec.h.
// Each type has the same memory layout as the kernel struct it is named
// for, because an ioctl copies the struct byte for byte between this
// process and the kernel. A misplaced field does not make the call
// fail: the kernel reads the wrong bytes and acts on them.
// kernel_test.go checks each size, offset, and request number against
// values compiled from the header.
//
// https://docs.kernel.org/userspace-api/media/cec/cec-api.html
// documents each ioctl and each field.

// maxMessageSize is the largest CEC message: a header byte, an opcode
// byte, and up to 14 operand bytes.
const maxMessageSize = 16

// KernelMsg is struct cec_msg, the argument of CEC_TRANSMIT and
// CEC_RECEIVE. Msg holds the message bytes. The other fields set how
// the kernel sends it, and report what happened on the bus.
type KernelMsg struct {
	TxTimestamp   uint64
	RxTimestamp   uint64
	Len           uint32
	Timeout       uint32
	Sequence      uint32
	Flags         uint32
	Msg           [maxMessageSize]byte
	Reply         uint8
	RxStatus      uint8
	TxStatus      uint8
	TxArbLostCnt  uint8
	TxNackCnt     uint8
	TxLowDriveCnt uint8
	TxErrorCnt    uint8
	_             [1]byte
}

// KernelCaps is struct cec_caps, the result of CEC_ADAP_G_CAPS.
type KernelCaps struct {
	Driver            [32]byte
	Name              [32]byte
	AvailableLogAddrs uint32
	Capabilities      uint32
	Version           uint32
}

// maxLogicalAddresses is the most logical addresses one adapter can
// claim.
const maxLogicalAddresses = 4

// KernelLogAddrs is struct cec_log_addrs: the logical addresses an
// adapter claims and the identity it announces with them. The kernel
// fills LogAddr and LogAddrMask, and the caller fills the other fields.
type KernelLogAddrs struct {
	LogAddr           [maxLogicalAddresses]uint8
	LogAddrMask       uint16
	CECVersion        uint8
	NumLogAddrs       uint8
	VendorID          uint32
	Flags             uint32
	OSDName           [15]byte
	PrimaryDeviceType [maxLogicalAddresses]uint8
	LogAddrType       [maxLogicalAddresses]uint8
	AllDeviceTypes    [maxLogicalAddresses]uint8
	Features          [maxLogicalAddresses][12]uint8
	_                 [1]byte
}

// KernelEvent is struct cec_event. Its final union holds a state change
// or a count of lost messages. Raw is the union's raw member, the
// widest, so it covers the whole union.
type KernelEvent struct {
	Timestamp uint64
	Event     uint32
	Flags     uint32
	Raw       [16]uint32
}

// An ioctl request number packs the copy direction into bits 30 and 31,
// the size of the argument into bits 16 to 29, a type letter into bits
// 8 to 15, and a command number into bits 0 to 7. These constants build
// numbers the way the kernel's _IOR, _IOW, and _IOWR macros do. The CEC
// API uses the letter 'a'. The size is that of the struct the request
// copies, which kernel_test.go checks against the Go types.
const (
	iocWrite     = 1 << 30
	iocRead      = 2 << 30
	iocReadWrite = iocRead | iocWrite
	iocType      = 'a' << 8
)

const (
	RequestGetCaps      uintptr = iocReadWrite | 76<<16 | iocType | 0
	RequestGetPhysAddr  uintptr = iocRead | 2<<16 | iocType | 1
	RequestSetPhysAddr  uintptr = iocWrite | 2<<16 | iocType | 2
	RequestGetLogAddrs  uintptr = iocRead | 92<<16 | iocType | 3
	RequestSetLogAddrs  uintptr = iocReadWrite | 92<<16 | iocType | 4
	RequestTransmit     uintptr = iocReadWrite | 56<<16 | iocType | 5
	RequestReceive      uintptr = iocReadWrite | 56<<16 | iocType | 6
	RequestDequeueEvent uintptr = iocReadWrite | 80<<16 | iocType | 7
	RequestGetMode      uintptr = iocRead | 4<<16 | iocType | 8
	RequestSetMode      uintptr = iocWrite | 4<<16 | iocType | 9
)

// requestNames maps each request to the name the kernel documentation
// uses, for error messages.
var requestNames = map[uintptr]string{
	RequestGetCaps:      "CEC_ADAP_G_CAPS",
	RequestGetPhysAddr:  "CEC_ADAP_G_PHYS_ADDR",
	RequestSetPhysAddr:  "CEC_ADAP_S_PHYS_ADDR",
	RequestGetLogAddrs:  "CEC_ADAP_G_LOG_ADDRS",
	RequestSetLogAddrs:  "CEC_ADAP_S_LOG_ADDRS",
	RequestTransmit:     "CEC_TRANSMIT",
	RequestReceive:      "CEC_RECEIVE",
	RequestDequeueEvent: "CEC_DQEVENT",
	RequestGetMode:      "CEC_G_MODE",
	RequestSetMode:      "CEC_S_MODE",
}

// The status bits the kernel sets in TxStatus and RxStatus of a
// KernelMsg.
const (
	TxStatusOK         = 1 << 0
	TxStatusArbLost    = 1 << 1
	TxStatusNack       = 1 << 2
	TxStatusLowDrive   = 1 << 3
	TxStatusError      = 1 << 4
	TxStatusMaxRetries = 1 << 5
	TxStatusAborted    = 1 << 6
	TxStatusTimeout    = 1 << 7

	RxStatusOK           = 1 << 0
	RxStatusTimeout      = 1 << 1
	RxStatusFeatureAbort = 1 << 2
	RxStatusAborted      = 1 << 3
)

// The modes that CEC_S_MODE sets on a file handle. The low nibble
// chooses whether the handle may send, and the high nibble chooses
// which received messages the kernel passes to it.
const (
	modeNoInitiator   = 0x0
	modeInitiator     = 0x1
	modeExclInitiator = 0x2
	modeNoFollower    = 0x0 << 4
	modeFollower      = 0x1 << 4
	modeMonitor       = 0xe << 4
	modeMonitorAll    = 0xf << 4
)

// The adapter capability bits that CEC_ADAP_G_CAPS reports.
const (
	CapPhysAddr    Capability = 1 << 0
	CapLogAddrs    Capability = 1 << 1
	CapTransmit    Capability = 1 << 2
	CapPassthrough Capability = 1 << 3
	CapRC          Capability = 1 << 4
	CapMonitorAll  Capability = 1 << 5
)

// logAddrsAllowRCPassthrough is CEC_LOG_ADDRS_FL_ALLOW_RC_PASSTHRU, the
// flag of KernelLogAddrs that makes the kernel pass the TV remote's
// key presses to the adapter's input device.
const logAddrsAllowRCPassthrough = 1 << 1

// The event types that CEC_DQEVENT returns.
const (
	EventStateChange = 1
	EventLostMsgs    = 2
)

// vendorIDNone is the vendor ID an adapter announces when it has none.
const vendorIDNone = 0xffffffff
