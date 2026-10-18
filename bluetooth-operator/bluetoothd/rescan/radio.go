package main

// What the program reads from the monitor channel for one adapter.
//
// A radio holds the three facts that main.go names: the auto connect
// devices, the connection attempt in progress, and the repairs that
// wait for their deadline. observe updates them from one packet. It
// reads only the packets below, and skips every other packet.

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/liken-sh/bluetooth-operator/bluetoothd/mgmt"
)

// The HCI commands and events the program reads, from the Bluetooth
// Core Specification, Volume 4, Part E. Every field is little-endian.
const (
	hciLESetScanEnable          = 0x200c
	hciLECreateConnection       = 0x200d
	hciLECreateConnectionCancel = 0x200e
	hciLESetExtScanEnable       = 0x2042
	hciLEExtCreateConnection    = 0x2043

	hciEvCommandComplete = 0x0e
	hciEvCommandStatus   = 0x0f
	hciEvLEMeta          = 0x3e

	// The LE Meta subevents that end a connection attempt. The kernel
	// unmasks the enhanced ones when the controller supports them, and
	// the controller then sends those in place of the first.
	hciLEConnectionComplete           = 0x01
	hciLEEnhancedConnectionComplete   = 0x0a
	hciLEEnhancedConnectionCompleteV2 = 0x29
)

// device names a Low Energy device the way the Management API does:
// its address, most significant byte first, and its address type,
// mgmt.AddressLEPublic or mgmt.AddressLERandom.
type device struct {
	address [6]byte
	kind    uint8
}

func (d device) String() string {
	a := d.address
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", a[0], a[1], a[2], a[3], a[4], a[5])
}

// readDevice reads an address in the kernel's order, least significant
// byte first, with the Management API's address type.
func readDevice(raw []byte, kind uint8) device {
	var d device
	for i := range d.address {
		d.address[i] = raw[len(d.address)-1-i]
	}
	d.kind = kind
	return d
}

// hciDeviceKind maps an HCI peer address type to the Management API's.
// Types 0x02 and 0x03 are the identity addresses that a controller
// resolved, public and random.
func hciDeviceKind(peerType uint8) uint8 {
	if peerType&0x01 == 0 {
		return mgmt.AddressLEPublic
	}
	return mgmt.AddressLERandom
}

// failure is a connection attempt that ended without a connection.
type failure struct {
	device device
	status uint8
}

// repair is an Add Device that waits for its deadline.
type repair struct {
	due    time.Time
	status uint8
}

// radio is what the program knows about one adapter.
type radio struct {
	// autoConnect holds each device that an Add Device with the auto
	// connect action named, until a Remove Device names it.
	autoConnect map[device]bool

	// attempting is true from LE Create Connection until the attempt
	// ends. target is the device the attempt is for. It is nil for an
	// attempt that connects to whichever device on the accept list
	// advertises first.
	attempting bool
	target     *device

	// lastFailure is the last attempt that ended without a connection.
	// A Create Connection Cancel that completes after it starts that
	// device's wait again.
	lastFailure *failure

	// scanEnable holds the value that the last scan enable command of
	// each opcode sent, so its Command Complete says which way the scan
	// went.
	scanEnable map[uint16]uint8

	// repairs holds the devices that wait for their deadline.
	repairs map[device]repair

	// checks holds the devices that this program added again, until
	// the kernel scans or the deadline passes. A deadline that passes
	// with no scan means the Add Device did not start the scan, and the
	// log says so.
	checks map[device]time.Time
}

func newRadio() *radio {
	return &radio{
		autoConnect: map[device]bool{},
		scanEnable:  map[uint16]uint8{},
		repairs:     map[device]repair{},
		checks:      map[device]time.Time{},
	}
}

// observe updates the radio from one packet that arrived at now.
func (r *radio) observe(p packet, now time.Time) {
	switch p.kind {
	case monitorCtrlCommand:
		r.managementCommand(p.data)
	case monitorCommand:
		r.hciCommand(p.data)
	case monitorEvent:
		r.hciEvent(p.data, now)
	case monitorCloseIndex:
		// The adapter powered off. Power-on starts the scan by itself
		// (mgmt_power_on), so nothing that waits still applies.
		r.attempting, r.target, r.lastFailure = false, nil, nil
		clear(r.repairs)
		clear(r.checks)
	}
}

// managementCommand reads an Add Device or a Remove Device: a cookie
// that names the sending socket, the opcode, and the parameters.
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
			delete(r.autoConnect, d)
		}
	case opcode == mgmt.OpRemoveDevice && len(params) >= 7:
		d := readDevice(params, params[6])
		// The zero address removes every device. bluetoothd sends it
		// when it registers an adapter, before it adds the bonded ones.
		if d.address == [6]byte{} {
			clear(r.autoConnect)
			return
		}
		delete(r.autoConnect, d)
	}
}

// hciCommand reads a command that the kernel sent to the controller:
// the opcode, the length of the parameters, and the parameters.
func (r *radio) hciCommand(data []byte) {
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
	}
}

// startAttempt records an attempt. A filter policy of zero connects to
// the peer address, and any other connects to the accept list.
func (r *radio) startAttempt(filterPolicy, peerType uint8, address []byte) {
	r.attempting, r.target = true, nil
	if filterPolicy == 0 {
		d := readDevice(address, hciDeviceKind(peerType))
		r.target = &d
	}
	// The kernel is connecting, so it decides about the scan when this
	// attempt ends, and that end starts its own wait.
	clear(r.repairs)
}

// hciEvent reads an event that the controller sent: the event code,
// the length of the parameters, and the parameters.
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
	case code == hciEvLEMeta && len(params) >= 2:
		switch params[0] {
		case hciLEConnectionComplete, hciLEEnhancedConnectionComplete, hciLEEnhancedConnectionCompleteV2:
			if r.attempting {
				r.endAttempt(params[1], now)
			}
		}
	}
}

func (r *radio) commandComplete(opcode uint16, status uint8, now time.Time) {
	switch opcode {
	case hciLESetScanEnable, hciLESetExtScanEnable:
		if status == 0 && r.scanEnable[opcode] == 1 {
			// The kernel scans, so no device waits for a repair.
			clear(r.repairs)
			clear(r.checks)
		}
	case hciLECreateConnectionCancel:
		// The kernel cancelled an attempt. When the attempt had already
		// failed, the cancel is the kernel ending the failed connection,
		// and its rescan may follow now. When the attempt was still in
		// progress, the cancel ends it.
		if r.attempting {
			r.endAttempt(0x02, now)
			return
		}
		if r.lastFailure != nil {
			r.arm(*r.lastFailure, now)
		}
	}
}

// endAttempt ends the attempt in progress with status. Status zero is
// a connection, which needs no repair.
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
	r.arm(f, now)
}

// arm starts the wait for one failed device, when bluetoothd added it
// for auto connect.
func (r *radio) arm(f failure, now time.Time) {
	if !r.autoConnect[f.device] {
		return
	}
	r.repairs[f.device] = repair{due: now.Add(rescanSettle), status: f.status}
}

// next returns the earliest deadline of a repair or a check.
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
