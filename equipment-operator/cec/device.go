package cec

// A Device is one CEC adapter, opened through the kernel's CEC API. It
// turns each ioctl into a Go call with Go types, and it reports every
// kernel error with the name of the ioctl and the kernel's own text.

import (
	"bytes"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A Handle is the ioctl boundary: one open /dev/cecN, or a fake of it.
// Ioctl passes one request and a pointer to its argument, the way the
// system call does. Wait blocks until the adapter has a message or an
// event for this handle, or until the timeout ends.
type Handle interface {
	Ioctl(request uintptr, argument unsafe.Pointer) error
	Wait(timeout time.Duration) (Readiness, error)
	Close() error
}

// Readiness is what Wait found: a received message to read with
// Receive, an event to read with NextEvent, or both.
type Readiness struct {
	Message bool
	Event   bool
}

// Device is an open adapter.
type Device struct {
	handle Handle
}

// New wraps a handle. Open is the usual way to get a Device; a test
// hands in a fake handle here.
func New(handle Handle) *Device {
	return &Device{handle: handle}
}

// Close releases the file handle. The kernel keeps the logical
// addresses the adapter claimed after the handle closes, so another
// program finds the adapter configured.
func (d *Device) Close() error {
	return d.handle.Close()
}

// Wait blocks until the adapter has something for this handle, or the
// timeout ends.
func (d *Device) Wait(timeout time.Duration) (Readiness, error) {
	return d.handle.Wait(timeout)
}

// ioctl makes one call and names it in the error. The wrapped error is
// the kernel's errno, so errors.Is finds ENODEV through it.
func (d *Device) ioctl(request uintptr, argument unsafe.Pointer) error {
	if err := d.handle.Ioctl(request, argument); err != nil {
		return fmt.Errorf("%s: %w", requestNames[request], err)
	}
	return nil
}

// IsGone answers whether an error says the adapter left. When a USB
// adapter is unplugged, the kernel removes /dev/cecN and every call on
// a handle to it fails with ENODEV. The handle never recovers, so the
// program that holds it exits and a restart opens the adapter again.
func IsGone(err error) bool {
	return errors.Is(err, unix.ENODEV)
}

// Capability is the capability bitmask CEC_ADAP_G_CAPS reports.
type Capability uint32

// Has answers whether every bit of the wanted mask is set.
func (c Capability) Has(want Capability) bool {
	return c&want == want
}

// Caps is what the adapter's driver says about the adapter.
type Caps struct {
	Driver string
	Name   string
	// AvailableLogicalAddresses is how many logical addresses the
	// adapter can hold at once. A Pulse-Eight holds one.
	AvailableLogicalAddresses int
	Capabilities              Capability
}

// Caps reads the adapter's driver, name, and capabilities.
func (d *Device) Caps() (Caps, error) {
	var raw KernelCaps
	if err := d.ioctl(RequestGetCaps, unsafe.Pointer(&raw)); err != nil {
		return Caps{}, err
	}
	return Caps{
		Driver:                    cString(raw.Driver[:]),
		Name:                      cString(raw.Name[:]),
		AvailableLogicalAddresses: int(raw.AvailableLogAddrs),
		Capabilities:              Capability(raw.Capabilities),
	}, nil
}

// cString reads a NUL-terminated C string out of a fixed array.
func cString(raw []byte) string {
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		raw = raw[:end]
	}
	return string(raw)
}

// PhysicalAddress reads the address the adapter announces now. An
// adapter that has none reports f.f.f.f.
func (d *Device) PhysicalAddress() (PhysicalAddress, error) {
	var raw uint16
	if err := d.ioctl(RequestGetPhysAddr, unsafe.Pointer(&raw)); err != nil {
		return InvalidPhysicalAddress, err
	}
	return PhysicalAddress(raw), nil
}

// SetPhysicalAddress sets the address the adapter announces. Only an
// adapter with no EDID of its own takes an address this way; the
// driver of an adapter built into a video port sets it from the EDID,
// and the kernel refuses the call with ENOTTY. A new address makes the
// kernel claim the logical addresses again.
func (d *Device) SetPhysicalAddress(address PhysicalAddress) error {
	raw := uint16(address)
	return d.ioctl(RequestSetPhysAddr, unsafe.Pointer(&raw))
}

// Claim is the identity an adapter announces on the bus: its device
// type, its OSD name, and whether the TV remote's buttons also become
// key events on the adapter's input device. The type defaults to a
// playback device, which is what a machine that shows video is.
type Claim struct {
	Type        DeviceType
	OSDName     string
	Passthrough bool
}

// claimTypes maps a device type to the three numbers a claim states
// for it: the kind of logical address to claim, the primary device
// type Report Physical Address carries, and the CEC 2.0 all device
// types bit.
var claimTypes = map[DeviceType]struct {
	logAddrType, primary, all byte
}{
	TypeTV:          {0, 0, 0x80},
	TypePlayback:    {3, 4, 0x10},
	TypeAudioSystem: {4, 5, 0x08},
}

// maxOSDName is the longest OSD name CEC carries: fourteen bytes of
// operand after the header and the opcode.
const maxOSDName = 14

// Claim asks the kernel to claim one logical address of the claim's
// type for the adapter. For a playback device the kernel polls 4, 8,
// and 11 in turn and takes the first that no device acknowledges. On a
// blocking handle the call returns when the claim is done. With no
// valid physical address the kernel stores the request and claims the
// address once one is set.
//
// The kernel answers Give Physical Address, Give OSD Name, and Get CEC
// Version for the claimed address by itself, from what this call
// states. It answers Give Device Vendor ID only for an adapter that
// states a vendor, and this call states none.
func (d *Device) Claim(claim Claim) error {
	kind := claim.Type
	if kind == "" {
		kind = TypePlayback
	}
	numbers, known := claimTypes[kind]
	if !known {
		return fmt.Errorf("claiming a logical address: this package does not claim the type %q", kind)
	}
	raw := KernelLogAddrs{
		CECVersion:  byte(Version14),
		NumLogAddrs: 1,
		VendorID:    vendorIDNone,
	}
	name := claim.OSDName
	if len(name) > maxOSDName {
		name = name[:maxOSDName]
	}
	copy(raw.OSDName[:], name)
	raw.LogAddrType[0] = numbers.logAddrType
	raw.PrimaryDeviceType[0] = numbers.primary
	raw.AllDeviceTypes[0] = numbers.all
	if claim.Passthrough {
		raw.Flags |= logAddrsAllowRCPassthrough
	}
	return d.ioctl(RequestSetLogAddrs, unsafe.Pointer(&raw))
}

// Release clears every logical address the adapter holds. The adapter
// then answers nothing on the bus. For a Pulse-Eight this also turns
// off the firmware's autonomous mode, which answers the TV without the
// host.
func (d *Device) Release() error {
	var raw KernelLogAddrs
	return d.ioctl(RequestSetLogAddrs, unsafe.Pointer(&raw))
}

// Addresses is what the adapter holds now: its logical addresses and
// the OSD name it announces.
type Addresses struct {
	Logical []LogicalAddress
	OSDName string
}

// Addresses reads the logical addresses the adapter holds. The list is
// empty while the adapter is not configured or still claiming.
func (d *Device) Addresses() (Addresses, error) {
	var raw KernelLogAddrs
	if err := d.ioctl(RequestGetLogAddrs, unsafe.Pointer(&raw)); err != nil {
		return Addresses{}, err
	}
	held := Addresses{OSDName: cString(raw.OSDName[:])}
	for index := 0; index < int(raw.NumLogAddrs) && index < maxLogicalAddresses; index++ {
		if raw.LogAddr[index] <= byte(AddressUnregistered) {
			held.Logical = append(held.Logical, LogicalAddress(raw.LogAddr[index]))
		}
	}
	return held, nil
}

// Follow makes this handle the only one that may send, and a follower:
// the kernel passes it every directed message and broadcast that the
// kernel does not answer by itself. A follower owes a Feature Abort
// for each directed message it does not support, because the kernel
// sends that answer only when no follower exists.
func (d *Device) Follow() error {
	mode := uint32(modeExclInitiator | modeFollower)
	return d.ioctl(RequestSetMode, unsafe.Pointer(&mode))
}

// Monitor makes this handle a monitor that cannot send. With the
// adapter's monitor-all capability it receives every message on the
// bus, including messages between two other devices. Without it, it
// receives the broadcasts and the messages to the adapter's own
// addresses. The kernel refuses both monitor modes with EPERM to a
// process without CAP_NET_ADMIN.
func (d *Device) Monitor(all bool) error {
	mode := uint32(modeNoInitiator | modeMonitor)
	if all {
		mode = modeNoInitiator | modeMonitorAll
	}
	return d.ioctl(RequestSetMode, unsafe.Pointer(&mode))
}

// Leave makes this handle neither an initiator nor a follower, the
// mode a handle has when it opens. A program that stops takes this
// mode so that no mode of its own outlives it.
func (d *Device) Leave() error {
	mode := uint32(modeNoInitiator | modeNoFollower)
	return d.ioctl(RequestSetMode, unsafe.Pointer(&mode))
}

// Initiate makes this handle the only one that may send, with no
// follower role. Clearing the logical addresses needs an initiator,
// so a handle takes this mode before Release.
func (d *Device) Initiate() error {
	mode := uint32(modeExclInitiator | modeNoFollower)
	return d.ioctl(RequestSetMode, unsafe.Pointer(&mode))
}
