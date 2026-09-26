package cec

// A scan finds every device on the bus and asks each one who it is.
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

// questions are the requests a scan sends to each device, and the
// opcode of each answer.
var questions = []struct {
	build func(from, to LogicalAddress) Message
	reply Opcode
}{
	{GivePhysicalAddress, OpReportPhysicalAddr},
	{GiveOSDName, OpSetOSDName},
	{GiveDeviceVendorID, OpDeviceVendorID},
	{GetCECVersion, OpCECVersion},
	{GiveDevicePowerStatus, OpReportPowerStatus},
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
