package main

// Tests of the log lines for messages seen on the CEC bus: one line for
// each message a person would want to see, and none for traffic between
// other devices, a scan's replies, or the adapter's own commands.

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/equipment-operator/cec"
	"github.com/liken-sh/liken/equipment-operator/cec/cectest"
)

// streamingBox is how a line describes the device at logical 8. The
// wire has no peer at 8, so a scan does not find it, and the tests send
// its messages.
const streamingBox = `"Streaming Box" (logical 8, 1.5.0.0)`

// lastHeard is the last message that each test sends. The read loop
// handles messages in order, so once its line is in the log, every
// earlier message has been logged or skipped. It is a broadcast Active
// Source, so it also sets the TV's power to On in the directory.
var lastHeard = cec.ActiveSource(11, 0x3000)

// heardLines sends messages on the wire, waits for the log line of
// lastHeard, and returns the lines of the messages sent before it.
func heardLines(t *testing.T, log *logBuffer, wire *cectest.Bus, messages ...cec.Message) []string {
	t.Helper()
	earlier := len(linesWith(log, "(logical 11,"))
	for _, message := range append(messages, lastHeard) {
		wire.Send(message)
	}
	waitForLines(t, log, "(logical 11,", earlier+1)
	var heard []string
	marks := 0
	for _, line := range linesWith(log, "(logical ") {
		switch {
		case strings.Contains(line, "(logical 11,"):
			marks++
		case marks == earlier:
			heard = append(heard, line)
		}
	}
	return heard
}

// listeningOnDen runs the node workload in Listen on the den bus. It
// sends messages that write no line and give the directory the
// streaming box's name and the TV's physical address.
func listeningOnDen(t *testing.T) (*logBuffer, *cectest.Bus) {
	t.Helper()
	api := startCECAPI(t)
	wire := fakeCECBus()
	_, device := usbAdapter(wire)
	api.putBus(listenBus("den"))
	log := loggedNode(t, api, device)
	api.waitForEntry(t, "den", "node-1", func(entry CECAdapterStatus) bool { return entry.State == AdapterListening })
	mustDeepEqual(t, heardLines(t, log, wire,
		cec.SetOSDName(8, 0, "Streaming Box"),
		cec.ReportPhysicalAddress(8, 0x1500, 4),
		cec.ReportPhysicalAddress(0, 0x0000, 0),
		cec.GiveDeviceVendorID(0, 8),
		cec.Poll(0, 8),
	), []string(nil))
	return log, wire
}

func TestTheNodeLogsEachMessageAPersonNotices(t *testing.T) {
	const tv = "the TV (logical 0, 0.0.0.0, name not known yet)"
	cases := []struct {
		name     string
		messages []cec.Message
		want     []string
	}{
		{"active source", []cec.Message{cec.ActiveSource(8, 0x1500)}, []string{
			"CECBus den: " + streamingBox + " broadcast Active Source 1.5.0.0",
		}},
		{"inactive source", []cec.Message{cec.NewMessage(8, 0, cec.OpInactiveSource, 0x15, 0x00)}, []string{
			"CECBus den: " + streamingBox + " sent Inactive Source 1.5.0.0 to the TV (logical 0)",
		}},
		{"request active source", []cec.Message{cec.NewMessage(0, 15, cec.OpRequestActiveSource)}, []string{
			"CECBus den: " + tv + " broadcast Request Active Source",
		}},
		{"routing change", []cec.Message{cec.NewMessage(0, 15, cec.OpRoutingChange, 0x12, 0x00, 0x15, 0x00)}, []string{
			"CECBus den: " + tv + " broadcast Routing Change from 1.2.0.0 to 1.5.0.0",
		}},
		{"routing information", []cec.Message{cec.NewMessage(5, 15, cec.OpRoutingInformation, 0x15, 0x00)}, []string{
			"CECBus den: the audio system (logical 5, name and physical address not known yet) broadcast Routing Information 1.5.0.0",
		}},
		{"set stream path", []cec.Message{cec.NewMessage(0, 15, cec.OpSetStreamPath, 0x15, 0x00)}, []string{
			"CECBus den: " + tv + " broadcast Set Stream Path 1.5.0.0",
		}},
		{"image view on", []cec.Message{cec.ImageViewOn(8, 0)}, []string{
			"CECBus den: " + streamingBox + " sent Image View On to the TV (logical 0)",
		}},
		{"text view on", []cec.Message{cec.NewMessage(8, 0, cec.OpTextViewOn)}, []string{
			"CECBus den: " + streamingBox + " sent Text View On to the TV (logical 0)",
		}},
		{"standby to all", []cec.Message{cec.Standby(0, 15)}, []string{
			"CECBus den: " + tv + " broadcast Standby",
		}},
		{"standby to one", []cec.Message{cec.Standby(8, 0)}, []string{
			"CECBus den: " + streamingBox + " sent Standby to the TV (logical 0)",
		}},
		{"set system audio mode", []cec.Message{cec.NewMessage(5, 15, cec.OpSetSystemAudioMode, 1)}, []string{
			"CECBus den: the audio system (logical 5, name and physical address not known yet) broadcast Set System Audio Mode On",
		}},
		{"system audio mode request", []cec.Message{cec.NewMessage(0, 5, cec.OpSystemAudioModeRequest, 0x15, 0x00)}, []string{
			"CECBus den: " + tv + " sent System Audio Mode Request for 1.5.0.0 to the audio system (logical 5)",
		}},
		{"a change of power", []cec.Message{
			cec.ReportPowerStatus(0, 8, cec.PowerStandby),
			cec.ReportPowerStatus(0, 8, cec.PowerToOn),
		}, []string{
			"CECBus den: " + tv + " sent Report Power Status Standby to " + `"Streaming Box" (logical 8)`,
			"CECBus den: " + tv + " sent Report Power Status ToOn to " + `"Streaming Box" (logical 8)`,
		}},
		{"a repeated power", []cec.Message{
			cec.ReportPowerStatus(0, 8, cec.PowerStandby),
			cec.ReportPowerStatus(0, 8, cec.PowerStandby),
		}, []string{
			"CECBus den: " + tv + " sent Report Power Status Standby to " + `"Streaming Box" (logical 8)`,
		}},
		// lastHeard already set the TV's power to On in the directory, so a
		// report of On changes nothing.
		{"a power the directory already has", []cec.Message{cec.ReportPowerStatus(0, 8, cec.PowerOn)}, nil},
		{"a key sent to another device", []cec.Message{cec.NewMessage(0, 8, cec.OpUserControlPressed, 0x41)}, nil},
		{"traffic between devices", []cec.Message{
			cec.GiveOSDName(0, 5),
			cec.SetOSDName(5, 0, "AVR"),
			cec.DeviceVendorID(5, 0x0005cd),
			cec.GiveDevicePowerStatus(8, 0),
			cec.Poll(8, 5),
		}, nil},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				log, wire := listeningOnDen(t)

				mustDeepEqual(t, heardLines(t, log, wire, one.messages...), one.want)
			})
		})
	}
}

// In Control the kernel delivers to the adapter only broadcasts and
// messages addressed to it, so the Image View On to the TV and the key
// press to logical 8 write no line. The adapter's own Image View On
// writes only the Television's line for the command.
func TestAControllingNodeLogsWhatTheFollowerHears(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		api := startCECAPI(t)
		wire := busWithTV(televisionTV(cec.PowerStandby))
		_, device := usbAdapter(wire)
		api.putDisplay("acm-0001-receiver", "node-1", "1.3.0.0")
		api.putBus(controlBus("den", CECBusAdapter{Machine: "node-1", Display: "acm-0001-receiver"}))
		api.putTelevision(lounge(TelevisionOn))
		log := loggedNode(t, api, device)
		api.scanned(t, "node-1")
		waitForLines(t, log, "Television lounge", 1)

		heard := heardLines(t, log, wire,
			cec.ActiveSource(8, 0x1500),
			cec.ImageViewOn(8, 0),
			cec.NewMessage(0, 4, cec.OpUserControlPressed, 0x41),
			cec.NewMessage(0, 4, cec.OpUserControlReleased),
			cec.NewMessage(0, 8, cec.OpUserControlPressed, 0x42),
		)

		mustDeepEqual(t, heard, []string{
			`CECBus den: a playback device (logical 8, 1.5.0.0, name not known yet) broadcast Active Source 1.5.0.0`,
			`CECBus den: "TV" (logical 0, 0.0.0.0) sent User Control Pressed Volume Up to this adapter (logical 4)`,
		})
		if sent := linesWith(log, "Image View On"); len(sent) != 1 || !strings.HasPrefix(sent[0], "Television lounge:") {
			t.Errorf("the lines that name Image View On are %q, want the Television's line alone", sent)
		}
	})
}
