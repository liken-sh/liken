package main

// A radio is the state of one adapter that the program builds from the
// packets of the monitor channel: the auto connect devices, the
// connection attempt in progress, the links that are up, and the
// repairs scheduled for devices whose scan has not started again.
// main.go explains the kernel bugs that this state detects.

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bluetoothd/mgmt"
)

// The HCI commands and events that radio reads, from the Bluetooth
// Core Specification, Volume 4, Part E. All fields are little-endian.
const (
	hciDisconnect               = 0x0406
	hciLESetScanEnable          = 0x200c
	hciLECreateConnection       = 0x200d
	hciLECreateConnectionCancel = 0x200e
	hciLESetExtScanEnable       = 0x2042
	hciLEExtCreateConnection    = 0x2043

	hciEvDisconnectComplete = 0x05
	hciEvCommandComplete    = 0x0e
	hciEvCommandStatus      = 0x0f
	hciEvLEMeta             = 0x3e

	// The LE Meta subevents that end a connection attempt. When the
	// controller supports the enhanced subevents, the kernel enables
	// them and the controller sends them instead of the first one.
	hciLEConnectionComplete           = 0x01
	hciLEEnhancedConnectionComplete   = 0x0a
	hciLEEnhancedConnectionCompleteV2 = 0x29
)

// device identifies a Low Energy device as the Management API does:
// an address with the most significant byte first, and an address type
// of mgmt.AddressLEPublic or mgmt.AddressLERandom.
type device struct {
	address [6]byte
	kind    uint8
}

func (d device) String() string {
	a := d.address
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[0], a[1], a[2], a[3], a[4], a[5])
}

// readDevice builds a device from an address in HCI byte order, least
// significant byte first, and a Management API address type.
func readDevice(raw []byte, kind uint8) device {
	var d device
	for i := range d.address {
		d.address[i] = raw[len(d.address)-1-i]
	}
	d.kind = kind
	return d
}

// hciDeviceKind maps an HCI peer address type to a Management API
// address type. HCI types 0x02 and 0x03 are resolved identity
// addresses, public and random, so the low bit selects the type.
func hciDeviceKind(peerType uint8) uint8 {
	if peerType&0x01 == 0 {
		return mgmt.AddressLEPublic
	}
	return mgmt.AddressLERandom
}

// failure is a connection attempt that ended with a nonzero HCI
// status.
type failure struct {
	device device
	status uint8
}

// repair is a scheduled repair. At due, unless a scan has started
// first, the watcher sends Add Device for the device. cause is the
// event that stopped the scan, for the log.
type repair struct {
	due   time.Time
	cause string
}

// radio is the state of one adapter.
type radio struct {
	// autoConnect is the set of devices that an Add Device command
	// with the auto connect action added, until a Remove Device
	// command removes them.
	autoConnect map[device]bool

	// attempting is true from LE Create Connection until the attempt
	// ends. target is the device being connected to. It is nil when the
	// attempt uses the accept list and connects to the first listed
	// device that advertises.
	attempting bool
	target     *device

	// lastFailure is the last attempt that failed. A Create Connection
	// Cancel that completes after the failure reschedules its repair.
	lastFailure *failure

	// links maps the connection handle of each link that is up to its
	// device. An HCI Disconnect command carries only the handle, so
	// this map finds the device.
	links map[uint16]device

	// scanEnable is the Scan Enable value of the last scan command of
	// each opcode. Command Complete does not repeat the value, so
	// commandComplete reads it here to tell whether the scan turned on.
	scanEnable map[uint16]uint8

	repairs map[device]repair

	// checks maps each device that the program sent a repair for to a
	// deadline. When the deadline passes with no scan, the repair did
	// not start the scan, and the watcher logs that.
	checks map[device]time.Time
}

func newRadio() *radio {
	return &radio{
		autoConnect: map[device]bool{},
		scanEnable:  map[uint16]uint8{},
		links:       map[uint16]device{},
		repairs:     map[device]repair{},
		checks:      map[device]time.Time{},
	}
}

// observe updates the radio from one packet received at now.
func (r *radio) observe(p packet, now time.Time) {
	switch p.kind {
	case monitorCtrlCommand:
		r.managementCommand(p.data)
	case monitorCommand:
		r.hciCommand(p.data, now)
	case monitorEvent:
		r.hciEvent(p.data, now)
	case monitorCloseIndex:
		// The adapter powered off. The kernel starts the scan again at
		// power on (mgmt_power_on), so the scheduled repairs are not
		// needed.
		r.attempting, r.target, r.lastFailure = false, nil, nil
		clear(r.links)
		clear(r.repairs)
		clear(r.checks)
	}
}

// managementCommand reads an Add Device or Remove Device command. The
// data holds a 4-byte cookie that identifies the sending socket, the
// 2-byte opcode, and the parameters.
func (r *radio) managementCommand(data []byte) {
	if len(data) < 6 {
		return
	}
	opcode := binary.LittleEndian.Uint16(data[4:])
	params := data[6:]
	switch {
	case opcode == mgmt.OpAddDevice && len(params) >= 8:
		kind, action := params[6], params[7]
		if kind == mgmt.AddressBREDR {
			return
		}
		d := readDevice(params, kind)
		if action == mgmt.ActionAutoConnect {
			r.autoConnect[d] = true
		} else {
			// A repair sends this action first and auto connect right
			// after, so the device returns to the map with the next
			// packet.
			delete(r.autoConnect, d)
		}
	case opcode == mgmt.OpRemoveDevice && len(params) >= 7:
		d := readDevice(params, params[6])
		// The zero address removes every device. bluetoothd sends it
		// when it registers an adapter, before it adds the bonded
		// devices.
		if d.address == [6]byte{} {
			clear(r.autoConnect)
			clear(r.repairs)
			return
		}
		// Delete the pending repair too. Otherwise the repair would add
		// back a device that bluetoothd removed, as it does on unpair.
		delete(r.autoConnect, d)
		delete(r.repairs, d)
	}
}

// hciCommand reads a command that the kernel sent to the controller.
// The data holds a 2-byte opcode, a 1-byte parameter length, and the
// parameters.
func (r *radio) hciCommand(data []byte, now time.Time) {
	if len(data) < 3 {
		return
	}
	opcode := binary.LittleEndian.Uint16(data)
	params := data[3:]
	switch {
	case (opcode == hciLESetScanEnable || opcode == hciLESetExtScanEnable) && len(params) >= 1:
		r.scanEnable[opcode] = params[0]
	case opcode == hciLECreateConnection && len(params) >= 12:
		// Scan Interval, Scan Window, Initiator Filter Policy, Peer
		// Address Type, Peer Address.
		r.startAttempt(params[4], params[5], params[6:12])
	case opcode == hciLEExtCreateConnection && len(params) >= 9:
		// Initiator Filter Policy, Own Address Type, Peer Address
		// Type, Peer Address.
		r.startAttempt(params[0], params[2], params[3:9])
	case opcode == hciDisconnect && len(params) >= 2:
		// Connection Handle, Reason. When Disconnection Complete
		// arrives late, the kernel leaves the device off its list of
		// devices to reconnect, so schedule a repair. A scan before
		// the repair is due cancels it.
		handle := binary.LittleEndian.Uint16(params) & 0x0fff
		if d, up := r.links[handle]; up {
			r.arm(d, fmt.Sprintf("the host ended the link to %s", d), now)
		}
	}
}

// startAttempt records the start of a connection attempt. An initiator
// filter policy of zero connects to the given peer address. Any other
// policy connects to the accept list, so there is no single target.
func (r *radio) startAttempt(filterPolicy, peerType uint8, address []byte) {
	r.attempting, r.target = true, nil
	if filterPolicy == 0 {
		d := readDevice(address, hciDeviceKind(peerType))
		r.target = &d
	}
	// The kernel stopped the scan to make this attempt. If the attempt
	// fails, endAttempt schedules a new repair, so the repairs
	// scheduled before it are dropped.
	clear(r.repairs)
}

// hciEvent reads an event that the controller sent. The data holds a
// 1-byte event code, a 1-byte parameter length, and the parameters.
func (r *radio) hciEvent(data []byte, now time.Time) {
	if len(data) < 2 {
		return
	}
	code, params := data[0], data[2:]
	switch {
	case code == hciEvCommandComplete && len(params) >= 4:
		// Num HCI Command Packets, Command Opcode, Status.
		opcode, status := binary.LittleEndian.Uint16(params[1:]), params[3]
		r.commandComplete(opcode, status, now)
	case code == hciEvCommandStatus && len(params) >= 4:
		// Status, Num HCI Command Packets, Command Opcode.
		status, opcode := params[0], binary.LittleEndian.Uint16(params[2:])
		if (opcode == hciLECreateConnection || opcode == hciLEExtCreateConnection) && status != 0 {
			r.endAttempt(status, now)
		}
	case code == hciEvDisconnectComplete && len(params) >= 3:
		// Status, Connection Handle, Reason. This event does not cancel
		// a repair, because the kernel stops waiting for it after two
		// seconds and then ignores it without rescanning.
		if params[0] == 0 {
			delete(r.links, binary.LittleEndian.Uint16(params[1:])&0x0fff)
		}
	case code == hciEvLEMeta && len(params) >= 2:
		switch params[0] {
		case hciLEConnectionComplete, hciLEEnhancedConnectionComplete, hciLEEnhancedConnectionCompleteV2:
			r.linkUp(params)
			if r.attempting {
				r.endAttempt(params[1], now)
			}
		}
	}
}

// linkUp records the device of a successful connection. The three
// connection complete subevents start with the same fields: Status,
// Connection Handle, Role, Peer Address Type, and Peer Address.
func (r *radio) linkUp(params []byte) {
	if len(params) < 12 || params[1] != 0 {
		return
	}
	handle := binary.LittleEndian.Uint16(params[2:]) & 0x0fff
	r.links[handle] = readDevice(params[6:12], hciDeviceKind(params[5]))
}

func (r *radio) commandComplete(opcode uint16, status uint8, now time.Time) {
	switch opcode {
	case hciLESetScanEnable, hciLESetExtScanEnable:
		if status == 0 && r.scanEnable[opcode] == 1 {
			// The kernel is scanning, so no repair is needed.
			clear(r.repairs)
			clear(r.checks)
		}
	case hciLECreateConnectionCancel:
		// If the attempt is still in progress, the cancel ends it, and
		// the controller reports the cancelled attempt with status
		// 0x02, Unknown Connection Identifier. If the attempt already
		// failed, the cancel is the kernel's cleanup of the failed
		// connection, and the kernel can rescan after it, so the
		// repair is scheduled again from now.
		if r.attempting {
			r.endAttempt(0x02, now)
			return
		}
		if r.lastFailure != nil {
			r.armFailure(*r.lastFailure, now)
		}
	}
}

// endAttempt ends the attempt in progress with an HCI status. Status
// zero means the device connected, so no repair is needed.
func (r *radio) endAttempt(status uint8, now time.Time) {
	target := r.target
	r.attempting, r.target = false, nil
	if target == nil {
		return
	}
	if status == 0 {
		r.lastFailure = nil
		delete(r.repairs, *target)
		return
	}
	f := failure{device: *target, status: status}
	r.lastFailure = &f
	r.armFailure(f, now)
}

func (r *radio) armFailure(f failure, now time.Time) {
	r.arm(f.device, fmt.Sprintf("a connection to %s failed with HCI status 0x%02x", f.device, f.status), now)
}

// arm schedules a repair for d, but only when bluetoothd added d with
// the auto connect action. For any other device, the kernel's cleanup
// of a failed attempt starts the scan again by itself.
func (r *radio) arm(d device, cause string, now time.Time) {
	if !r.autoConnect[d] {
		return
	}
	r.repairs[d] = repair{due: now.Add(rescanSettle), cause: cause}
}

// next returns the earliest deadline of any repair or check, and false
// when there are none.
func (r *radio) next() (time.Time, bool) {
	var earliest time.Time
	for _, each := range r.repairs {
		if earliest.IsZero() || each.due.Before(earliest) {
			earliest = each.due
		}
	}
	for _, due := range r.checks {
		if earliest.IsZero() || due.Before(earliest) {
			earliest = due
		}
	}
	return earliest, !earliest.IsZero()
}
