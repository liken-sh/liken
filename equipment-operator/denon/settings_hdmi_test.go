// The HDMI setup family: the wire lines each field carries, the lines
// the receiver reports, and the query that reads a set back.

package denon

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

func TestTheHDMICommandBuildersWriteTheWireLines(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		build func() (string, error)
	}{
		{"audio out avr", "VSAUDIO AMP", func() (string, error) { return HDMIAudioOutCommand("avr") }},
		{"audio out tv", "VSAUDIO TV", func() (string, error) { return HDMIAudioOutCommand("tv") }},
		{"pass through on", "SSHOSPAS ON", func() (string, error) { return HDMIPassThroughCommand(true) }},
		{"pass through off", "SSHOSPAS OFF", func() (string, error) { return HDMIPassThroughCommand(false) }},
		{"pass through source last", "SSHOSCONSTS LAS", func() (string, error) { return HDMIPassThroughSourceCommand("last") }},
		{"pass through source hdmi1", "SSHOSCONSTS HD1", func() (string, error) { return HDMIPassThroughSourceCommand("hdmi1") }},
		{"pass through source hdmi7", "SSHOSCONSTS HD7", func() (string, error) { return HDMIPassThroughSourceCommand("hdmi7") }},
		{"rc source select power on", "SSHOSRSS POS", func() (string, error) { return HDMIRCSourceSelectCommand("powerOnAndSource") }},
		{"rc source select only", "SSHOSRSS SSO", func() (string, error) { return HDMIRCSourceSelectCommand("sourceSelectOnly") }},
		{"control on", "SSHOSCON ON", func() (string, error) { return HDMIControlCommand(true) }},
		{"control off", "SSHOSCON OFF", func() (string, error) { return HDMIControlCommand(false) }},
		{"arc on", "SSHOSCONARC ON", func() (string, error) { return HDMIARCCommand(true) }},
		{"arc off", "SSHOSCONARC OFF", func() (string, error) { return HDMIARCCommand(false) }},
		{"tv audio switching on", "SSHOSTAS ON", func() (string, error) { return HDMITVAudioSwitchingCommand(true) }},
		{"tv audio switching off", "SSHOSTAS OFF", func() (string, error) { return HDMITVAudioSwitchingCommand(false) }},
		{"power off control all", "SSHOSCONPOF ALL", func() (string, error) { return HDMIPowerOffControlCommand("all") }},
		{"power off control video", "SSHOSCONPOF VID", func() (string, error) { return HDMIPowerOffControlCommand("video") }},
		{"power off control off", "SSHOSCONPOF OFF", func() (string, error) { return HDMIPowerOffControlCommand("off") }},
		{"power saving on", "SSHOSCONPSV ON", func() (string, error) { return HDMIPowerSavingCommand(true) }},
		{"power saving off", "SSHOSCONPSV OFF", func() (string, error) { return HDMIPowerSavingCommand(false) }},
		{"an unknown audio out", "", func() (string, error) { return HDMIAudioOutCommand("amp") }},
		{"an unknown pass through source", "", func() (string, error) { return HDMIPassThroughSourceCommand("MPLAY") }},
		{"a pass through source past the jacks", "", func() (string, error) { return HDMIPassThroughSourceCommand("hdmi8") }},
		{"an unknown rc source select", "", func() (string, error) { return HDMIRCSourceSelectCommand("always") }},
		{"an unknown power off control", "", func() (string, error) { return HDMIPowerOffControlCommand("audio") }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, err := one.build()
			mustMatch(t, got, one.want)
			if one.want == "" {
				mustMatch(t, err != nil, true)
			} else {
				mustSucceed(t, err)
			}
		})
	}
}

// Every line the receiver reports for the HDMI setup folds into its
// field, and the SSHOS END line that closes the answer is known and
// changes nothing.
func TestTheHDMILinesFoldIntoTheirFields(t *testing.T) {
	cases := []struct {
		line  string
		check func(HDMISettings) bool
	}{
		{"VSAUDIO AMP", func(h HDMISettings) bool { return *h.AudioOut == "avr" }},
		{"VSAUDIO TV", func(h HDMISettings) bool { return *h.AudioOut == "tv" }},
		{"SSHOSPAS ON", func(h HDMISettings) bool { return *h.PassThrough }},
		{"SSHOSCONSTS LAS", func(h HDMISettings) bool { return *h.PassThroughSource == "last" }},
		{"SSHOSCONSTS HD3", func(h HDMISettings) bool { return *h.PassThroughSource == "hdmi3" }},
		{"SSHOSRSS POS", func(h HDMISettings) bool { return *h.RCSourceSelect == "powerOnAndSource" }},
		{"SSHOSRSS SSO", func(h HDMISettings) bool { return *h.RCSourceSelect == "sourceSelectOnly" }},
		{"SSHOSCON ON", func(h HDMISettings) bool { return *h.Control }},
		{"SSHOSCONARC OFF", func(h HDMISettings) bool { return !*h.ARC }},
		{"SSHOSTAS OFF", func(h HDMISettings) bool { return !*h.TVAudioSwitching }},
		{"SSHOSCONPOF ALL", func(h HDMISettings) bool { return *h.PowerOffControl == "all" }},
		{"SSHOSCONPOF VID", func(h HDMISettings) bool { return *h.PowerOffControl == "video" }},
		{"SSHOSCONPSV OFF", func(h HDMISettings) bool { return !*h.PowerSaving }},
		{"SSHOS END", func(h HDMISettings) bool { return h == HDMISettings{} }},
	}
	for _, one := range cases {
		t.Run(one.line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), one.line)
			mustMatch(t, known, true)
			mustMatch(t, one.check(state.Settings.HDMI), true)
		})
	}
}

// A word no builder accepts, or an SSHOS key this driver does not
// read, leaves the HDMI settings unset rather than hold a value the
// operator could never send back.
func TestAnUnknownHDMILineLeavesTheFieldsUnset(t *testing.T) {
	for _, line := range []string{
		"VSAUDIO BOTH", "SSHOSPAS MAYBE", "SSHOSCONSTS FRO", "SSHOSRSS NEVER",
		"SSHOSCON 1", "SSHOSCONARC", "SSHOSCONPOF AUDIO", "SSHOSALS ON",
	} {
		t.Run(line, func(t *testing.T) {
			state, _, _, known := applyDenonLine(newDenonState(), line)
			mustMatch(t, known, false)
			mustMatch(t, state.Settings.HDMI, HDMISettings{})
		})
	}
}

// Every word a builder accepts round-trips through the parser, so a
// value the operator sends is one the status can report.
func TestEveryHDMIBuilderWordRoundTripsThroughItsParser(t *testing.T) {
	words := []struct {
		build  func(string) (string, error)
		get    func(HDMISettings) *string
		values []string
	}{
		{HDMIAudioOutCommand, func(h HDMISettings) *string { return h.AudioOut }, []string{"avr", "tv"}},
		{HDMIPassThroughSourceCommand, func(h HDMISettings) *string { return h.PassThroughSource },
			[]string{"last", "hdmi1", "hdmi2", "hdmi3", "hdmi4", "hdmi5", "hdmi6", "hdmi7"}},
		{HDMIRCSourceSelectCommand, func(h HDMISettings) *string { return h.RCSourceSelect },
			[]string{"powerOnAndSource", "sourceSelectOnly"}},
		{HDMIPowerOffControlCommand, func(h HDMISettings) *string { return h.PowerOffControl },
			[]string{"all", "video", "off"}},
	}
	for _, family := range words {
		for _, value := range family.values {
			t.Run(value, func(t *testing.T) {
				command, err := family.build(value)
				mustSucceed(t, err)
				state, _, _, known := applyDenonLine(newDenonState(), command)
				mustMatch(t, known, true)
				mustMatch(t, *family.get(state.Settings.HDMI), value)
			})
		}
	}
}

// The receiver takes an HDMI setup command and does not echo it, so
// the driver asks for the family again after the sets. The fake
// receiver behaves the same way: it holds the value and reports it only
// when asked. Each field travels the whole apply path and reads back.
func TestApplySettingsRoundTripsEveryHDMIField(t *testing.T) {
	cases := []struct {
		name    string
		want    HDMISettings
		command string
		query   string
		check   func(HDMISettings) bool
	}{
		{"audio out", HDMISettings{AudioOut: strPtr("tv")}, "VSAUDIO TV", "VSAUDIO ?",
			func(h HDMISettings) bool { return h.AudioOut != nil && *h.AudioOut == "tv" }},
		{"pass through", HDMISettings{PassThrough: boolPtr(false)}, "SSHOSPAS OFF", "SSHOS ?",
			func(h HDMISettings) bool { return h.PassThrough != nil && !*h.PassThrough }},
		{"pass through source", HDMISettings{PassThroughSource: strPtr("hdmi2")}, "SSHOSCONSTS HD2", "SSHOS ?",
			func(h HDMISettings) bool { return h.PassThroughSource != nil && *h.PassThroughSource == "hdmi2" }},
		{"rc source select", HDMISettings{RCSourceSelect: strPtr("sourceSelectOnly")}, "SSHOSRSS SSO", "SSHOS ?",
			func(h HDMISettings) bool { return h.RCSourceSelect != nil && *h.RCSourceSelect == "sourceSelectOnly" }},
		{"control", HDMISettings{Control: boolPtr(false)}, "SSHOSCON OFF", "SSHOS ?",
			func(h HDMISettings) bool { return h.Control != nil && !*h.Control }},
		{"arc", HDMISettings{ARC: boolPtr(true)}, "SSHOSCONARC ON", "SSHOS ?",
			func(h HDMISettings) bool { return h.ARC != nil && *h.ARC }},
		{"tv audio switching", HDMISettings{TVAudioSwitching: boolPtr(true)}, "SSHOSTAS ON", "SSHOS ?",
			func(h HDMISettings) bool { return h.TVAudioSwitching != nil && *h.TVAudioSwitching }},
		{"power off control", HDMISettings{PowerOffControl: strPtr("video")}, "SSHOSCONPOF VID", "SSHOS ?",
			func(h HDMISettings) bool { return h.PowerOffControl != nil && *h.PowerOffControl == "video" }},
		{"power saving", HDMISettings{PowerSaving: boolPtr(true)}, "SSHOSCONPSV ON", "SSHOS ?",
			func(h HDMISettings) bool { return h.PowerSaving != nil && *h.PowerSaving }},
	}
	t.Parallel()
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				harness := startHarness(t)
				drainQueries(t, harness)

				mustSucceed(t, harness.client.ApplySettings(Settings{HDMI: one.want}))
				sent := harness.receiver.waitForCommands(t, one.query)
				mustMatch(t, sent[0], one.command)

				waitForSettings(t, harness.client, func(s Settings) bool { return one.check(s.HDMI) })
			})
		})
	}
}

// Several HDMI fields in one apply send one read-back per family, after
// every set.
func TestApplySettingsAsksForTheHDMIFamiliesOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		drainQueries(t, harness)

		want := Settings{HDMI: HDMISettings{
			AudioOut:          strPtr("avr"),
			PassThroughSource: strPtr("last"),
			Control:           boolPtr(true),
		}}
		mustSucceed(t, harness.client.ApplySettings(want))

		sent := harness.receiver.waitForCommands(t, "SSHOS ?")
		mustMatch(t, strings.Join(sent, " | "), "VSAUDIO AMP | SSHOSCONSTS LAS | SSHOSCON ON | VSAUDIO ? | SSHOS ?")
	})
}

// A keyed bus write to an HDMI id sends the set and then the read-back.
func TestSetRoutesAnHDMIIdToTheWireAndReadsItBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		drainQueries(t, harness)

		mustSucceed(t, harness.client.Set("hdmi.passThroughSource", equipment.StringSettingValue("hdmi4")))

		sent := harness.receiver.waitForCommands(t, "SSHOS ?")
		mustMatch(t, strings.Join(sent, " | "), "SSHOSCONSTS HD4 | SSHOS ?")
		got := waitForSettings(t, harness.client, func(s Settings) bool {
			return s.HDMI.PassThroughSource != nil && *s.HDMI.PassThroughSource == "hdmi4"
		})
		mustMatch(t, *got.HDMI.PassThroughSource, "hdmi4")
	})
}

// A set that sends no HDMI command sends no HDMI read-back.
func TestSetOutsideTheHDMIFamilySendsNoReadBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		harness := startHarness(t)
		drainQueries(t, harness)

		mustSucceed(t, harness.client.Set("tone.bass", equipment.NumberSettingValue(3)))
		mustMatch(t, harness.receiver.waitForCommand(t), "PSBAS 53")

		mustStaySilentCommands(t, harness.receiver.commands, 100*time.Millisecond)
	})
}

// A read-back that cannot be delivered is reported, the way a set that
// cannot be delivered is.
func TestTheHDMIReadBackOnADisconnectedClientReportsTheFailure(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	err := client.sendReadBacks([]string{"SSHOSCON ON"})
	if err == nil {
		t.Fatal("a read-back on a disconnected client did not error")
	}
}
