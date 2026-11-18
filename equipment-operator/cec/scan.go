package cec

// CEC has no command that lists the devices on the bus. A device
// acknowledges a poll to its own logical address, and an address with
// no device leaves the acknowledge bit unset, so a scan polls each
// address in turn, as cec-ctl --show-topology does.
//
// HDMI-CEC 1.3a, CEC 13.15.2, says that a source that implements System
// Audio Control reads the System Audio Mode when it joins the bus. So
// the scan reads it from the audio system after its other queries, and
// the broadcasts of Set System Audio Mode keep it current after that.

import (
	"time"
)

// replyTimeout is one second because the CEC supplement allows a
// device one second to reply, and the kernel's default is the same.
const replyTimeout = time.Second

// ScanReport is the result of a Scan.
type ScanReport struct {
	// Acked is the number of addresses that acknowledged a poll.
	Acked int
}

// PowerReader reads the TV's power status and records it in the
// directory. Scan and Introduce call it in place of sending Give Device
// Power Status to the TV. When two requests for the TV's power are in
// flight at once, the kernel delivers the one Report Power Status to
// only one of them. So a caller that also reads the TV's power
// elsewhere, such as for a powerAsk, sends every read
// through one PowerReader that joins a read already in flight. An error
// from the reader stops the scan or introduction.
type PowerReader func() error

func usesPowerReader(reply Opcode, address LogicalAddress, read PowerReader) bool {
	return read != nil && address == AddressTV && reply == OpReportPowerStatus
}

// queries are the requests that a scan sends to each device. known
// reports whether the directory already has the fact that the reply
// gives, so Introduce can skip the request.
var queries = []struct {
	build func(from, to LogicalAddress) Message
	reply Opcode
	known func(Peer) bool
}{
	{GivePhysicalAddress, OpReportPhysicalAddr, func(p Peer) bool { return p.Physical != InvalidPhysicalAddress }},
	{GiveOSDName, OpSetOSDName, func(p Peer) bool { return p.OSDName != "" }},
	{GiveDeviceVendorID, OpDeviceVendorID, func(p Peer) bool { return p.Vendor != VendorUnknown || p.NoVendor }},
	{GetCECVersion, OpCECVersion, func(p Peer) bool { return p.Version != VersionUnknown }},
	{GiveDevicePowerStatus, OpReportPowerStatus, func(p Peer) bool { return p.Power != PowerUnknown }},
}

// Scan polls every logical address except own, queries each device
// that acknowledges, and records the replies in the directory. Only a
// NACK removes an address from the directory. A poll that loses
// arbitration or meets a low drive or another error says nothing about
// whether a device is there, so the entry stays. A device that does not
// reply to a query also stays, because a device in deep standby can
// acknowledge a poll and reply to nothing else. An error from a
// transmit or from tvPower stops the scan. tvPower can be nil.
func Scan(device *Device, directory *Directory, own LogicalAddress, tvPower PowerReader) (ScanReport, error) {
	report := ScanReport{}
	for address := AddressTV; address < AddressUnregistered; address++ {
		if address == own {
			continue
		}
		result, err := device.Transmit(Poll(own, address), 0, 0)
		if err != nil {
			return report, err
		}
		if result.Nacked {
			directory.Forget(address)
			continue
		}
		if !result.Acked {
			continue
		}
		report.Acked++
		directory.Present(address)
		for _, query := range queries {
			if usesPowerReader(query.reply, address, tvPower) {
				if err := tvPower(); err != nil {
					return report, err
				}
				continue
			}
			result, err := device.Transmit(query.build(own, address), query.reply, replyTimeout)
			if err != nil {
				return report, err
			}
			if result.Reply != nil {
				directory.Observe(*result.Reply)
			}
		}
		if address == AddressAudioSystem {
			if _, _, err := ReadSystemAudioMode(device, directory, own); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}

// Introduce queries one device for each fact that the directory does
// not have yet, and returns the number of queries. It is for a device
// that announced itself after the scan, such as with the Report
// Physical Address that the kernel broadcasts after a claim, because
// that broadcast gives only some of the device's facts. A NACK means
// the device left, so Introduce removes it from the directory. An error
// from a transmit or from tvPower stops it. tvPower can be nil.
func Introduce(device *Device, directory *Directory, own, address LogicalAddress, tvPower PowerReader) (int, error) {
	asked := 0
	if address == own || address == AddressUnregistered {
		return asked, nil
	}
	for _, query := range queries {
		if query.known(directory.Lookup(address)) {
			continue
		}
		asked++
		if usesPowerReader(query.reply, address, tvPower) {
			if err := tvPower(); err != nil {
				return asked, err
			}
			continue
		}
		result, err := device.Transmit(query.build(own, address), query.reply, replyTimeout)
		if err != nil {
			return asked, err
		}
		if result.Nacked {
			directory.Forget(address)
			return asked, nil
		}
		if result.Reply != nil {
			directory.Observe(*result.Reply)
		}
	}
	return asked, nil
}
