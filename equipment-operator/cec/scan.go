package cec

// A scan finds every device on the bus and asks each one who it is. An
// introduction asks one device that announced itself only what the
// directory does not hold yet.
// CEC has no list of devices, so the scan polls each logical address:
// a device acknowledges a poll to its own address, and an address
// with no device leaves the acknowledge bit unset. Then the scan asks
// each device that answered for its physical address, its OSD name,
// its vendor, its CEC version, and its power status. cec-ctl
// --show-topology does the same.

import (
	"time"
)

// replyTimeout is how long a question waits for its answer. The CEC
// supplement gives a device one second to answer a request, and the
// kernel uses the same default.
const replyTimeout = time.Second

// A ScanReport says what one scan found.
type ScanReport struct {
	// Acked counts the addresses that acknowledged a poll.
	Acked int
}

// questions are the requests a scan sends to each device, the opcode
// of each answer, and whether the directory already holds the fact the
// answer states.
var questions = []struct {
	build func(from, to LogicalAddress) Message
	reply Opcode
	known func(Peer) bool
}{
	{GivePhysicalAddress, OpReportPhysicalAddr, func(p Peer) bool { return p.Physical != InvalidPhysicalAddress }},
	{GiveOSDName, OpSetOSDName, func(p Peer) bool { return p.OSDName != "" }},
	{GiveDeviceVendorID, OpDeviceVendorID, func(p Peer) bool { return p.Vendor != VendorUnknown }},
	{GetCECVersion, OpCECVersion, func(p Peer) bool { return p.Version != VersionUnknown }},
	{GiveDevicePowerStatus, OpReportPowerStatus, func(p Peer) bool { return p.Power != PowerUnknown }},
}

// Scan polls every logical address but the adapter's own and asks each
// device that answers for its facts. It records what it learns in the
// directory, and it forgets a peer whose poll the wire answers with a
// NACK. A poll that fails another way, by a lost arbitration, a low
// drive, or an error, keeps what the directory held for the address,
// because that failure says nothing about the device. A
// device that does not answer one question keeps what the directory
// held, because a device in deep standby can acknowledge a poll and
// still leave a request unanswered. An error means the kernel refused a
// call, and the scan stops there.
func Scan(device *Device, directory *Directory, own LogicalAddress) (ScanReport, error) {
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
		for _, question := range questions {
			result, err := device.Transmit(question.build(own, address), question.reply, replyTimeout)
			if err != nil {
				return report, err
			}
			if result.Reply != nil {
				directory.Observe(*result.Reply)
			}
		}
	}
	return report, nil
}

// Introduce asks one device for each fact the directory does not hold
// yet, and answers how many questions it sent. A device announces
// itself with a broadcast, such as the Report Physical Address the
// kernel sends after a claim, and the broadcast states only some of its
// facts. So an introduction replaces a new scan of the bus: it sends
// nothing to the other devices, and nothing to a device whose facts the
// directory already holds. A NACK means the device left, and the
// directory forgets it. A question that goes unanswered keeps what the
// directory held, as in a scan. An error means the kernel refused a
// call.
func Introduce(device *Device, directory *Directory, own, address LogicalAddress) (int, error) {
	asked := 0
	if address == own || address == AddressUnregistered {
		return asked, nil
	}
	for _, question := range questions {
		if question.known(directory.Lookup(address)) {
			continue
		}
		asked++
		result, err := device.Transmit(question.build(own, address), question.reply, replyTimeout)
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
