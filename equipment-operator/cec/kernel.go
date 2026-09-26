package cec

// The kernel's CEC API, as linux/cec.h states it. Each type here has
// the exact memory layout of the kernel struct of the same name,
// because an ioctl copies the struct byte for byte between this
// process and the kernel. A field out of place does not fail the
// call: the kernel reads the wrong bytes and answers something else.
// kernel_test.go pins every size and every request number against
// values compiled from the header.
//
// The reference is
// https://docs.kernel.org/userspace-api/media/cec/cec-api.html, which
// documents each ioctl and each field.

// The largest CEC message: one header byte, one opcode byte, and at
// most fourteen operand bytes.
const maxMessageSize = 16

// KernelMsg is struct cec_msg, the unit of CEC_TRANSMIT and
// CEC_RECEIVE. Msg holds the message itself; the other fields tell the
// kernel how to send it and tell this process what happened on the
// wire.
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

// KernelCaps is struct cec_caps, what CEC_ADAP_G_CAPS reports.
type KernelCaps struct {
	Driver            [32]byte
	Name              [32]byte
	AvailableLogAddrs uint32
	Capabilities      uint32
	Version           uint32
}

// The number of logical addresses one adapter can hold at most.
const maxLogicalAddresses = 4

// KernelLogAddrs is struct cec_log_addrs, the logical addresses an
// adapter claims and the identity it announces with them. The kernel
// fills LogAddr and LogAddrMask; the caller fills the rest.
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

// KernelEvent is struct cec_event. The union at its end holds a state
// change or a count of lost messages, and Raw is its widest member.
// StateChange reads the first member out of it.
type KernelEvent struct {
	Timestamp uint64
	Event     uint32
	Flags     uint32
	Raw       [16]uint32
}

// The ioctl request numbers. An ioctl number packs the direction of
// the copy in bits 30 and 31, the size of the argument in bits 16 to
// 29, a type letter in bits 8 to 15, and a number in bits 0 to 7, the
// same way the kernel's _IOR, _IOW, and _IOWR macros pack them. The
// CEC API uses the letter 'a'. The size is the size of the struct the
// request copies, which kernel_test.go checks against the Go types.
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

// requestNames names each request in an error, so a failure reads the
// way the kernel documentation spells the call.
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

// The transmit and receive status bits the kernel sets in a KernelMsg.
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

// The file handle modes CEC_S_MODE sets. The low nibble says whether
// this handle may send, and the high nibble says which received
// messages the kernel passes to it.
const (
	modeNoInitiator   = 0x0
	modeInitiator     = 0x1
	modeExclInitiator = 0x2
	modeNoFollower    = 0x0 << 4
	modeFollower      = 0x1 << 4
	modeMonitor       = 0xe << 4
	modeMonitorAll    = 0xf << 4
)

// The adapter capability bits CEC_ADAP_G_CAPS reports.
const (
	CapPhysAddr    Capability = 1 << 0
	CapLogAddrs    Capability = 1 << 1
	CapTransmit    Capability = 1 << 2
	CapPassthrough Capability = 1 << 3
	CapRC          Capability = 1 << 4
	CapMonitorAll  Capability = 1 << 5
)

// The flag that also passes the TV remote's buttons to the kernel's
// input device, which CEC_ADAP_S_LOG_ADDRS reads.
const logAddrsAllowRCPassthrough = 1 << 1

// The events CEC_DQEVENT reports.
const (
	EventStateChange = 1
	EventLostMsgs    = 2
)

// vendorIDNone is the vendor ID an adapter announces when it has none.
const vendorIDNone = 0xffffffff
