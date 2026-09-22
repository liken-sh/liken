// The client against a fake amp on a real TLS socket, plus the parsing
// and the setters.

package wiim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

func mustMatch(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The canned answers the fake amp serves. The values are invented; the
// field names and the shapes are what the live amps answer.
const (
	statusExJSON = `{ "language":"en_us", "DeviceName":"Test Amp", "GroupName":"Test Amp", "ssid":"Test Amp", "group":"0", "firmware":"Linkplay.5.2.1", "build":"release", "project":"WiiM_Amp_4layer", "Release":"20260101", "hardware":"AmlogicA113", "PCB_version":"7", "mcu_ver":"0", "hdmi_ver":"100", "uboot_verinfo":"250928.100906", "uuid":"FF98F2F7AABBCCDDEEFF0011", "MAC":"00:11:22:33:44:55", "BTMAC":"00:11:22:33:44:56", "AP_MAC":"00:11:22:33:44:57", "ETH_MAC":"00:11:22:33:44:58", "temp_uuid":"AABB", "upnp_uuid":"uuid:FF98F2F7-AABB-CCDD-EEFF-0011FF98F2F7", "internet":"1", "date":"2026:09:21", "time":"12:00:00", "tz":"-4.0", "app_timezone_id":"America/New_York", "privacy_mode":"0", "InitialConfiguration":"1", "communication_port":"8819", "cast_enable":1, "preset_key":"12", "mqtt_support":"1", "audiocast":"1", "max_volume":"100", "volume_control":"0", "EQ_support":"Eq10HP_ver_2.0", "EQVersion":"4.3", "security":"https/2.0", "security_version":"3.0", "security_capabilities":{"ver":"1.0","aes_ver":"1.0"}, "apcli0":"0.0.0.0", "eth0":"192.0.2.10", "essid":"54657374", "WifiChannel":"0", "RSSI":"0", "BSSID":"", "wlanSnr":"0", "wlanNoise":"0", "wlanFreq":"0", "wlanDataRate":"0", "temperature_cpu":"32", "temperature_tmp102":"27", "BleRemoteControl":"1", "BleRemoteConnected":"1", "BleRemoteBatterylevel":"100", "BleRemoteRSSI":"-57", "BleRemoteVersion":"0054", "BleRemote_dev_state":"normal" }`
	playerJSON   = `{"type":"0","ch":"0","mode":"43","loop":"4","eq":"0","vendor":"","status":"play","curpos":"1000","offset_pts":"0","totlen":"2000","Title":"486176652061204369676172","Artist":"50696E6B20466C6F7964","Album":"5769736820596F7520576572652048657265","alarmflag":"0","plicount":"0","plicurr":"0","vol":"31","mute":"0"}`
	metaJSON     = `{ "metaData": { "album":"Wish You Were Here", "title":"Have a Cigar", "subtitle":"unknown", "artist":"Pink Floyd", "albumArtURI":"https://example.invalid/art.jpg", "sampleRate":"44100", "bitDepth":"24", "bitRate":"1525", "trackId":"abc" } }`
)

// fakeAmp answers the commands a live amp answers, with one canned
// answer each.
type fakeAmp struct {
	server *httptest.Server

	mutex    sync.Mutex
	commands []string
	answers  map[string]string
	failing  int
	setter   string
}

func startFakeAmp(t *testing.T) *fakeAmp {
	t.Helper()
	amp := &fakeAmp{answers: map[string]string{
		statusCommand:       statusExJSON,
		playerCommand:       playerJSON,
		metaCommand:         metaJSON,
		balanceCommand:      "0.5",
		eqStatCommand:       `{"EQStat":"On"}`,
		eqListCommand:       `["Flat","Rock"]`,
		shutdownCommand:     "300",
		outputModeCommand:   `{"hardware":"7","source":"0","audiocast":"0"}`,
		outputListCommand:   `[{"index":1,"mode":"AUDIO_OUTPUT_SPEAKER_MODE","soundCard":{"cardName":"AMLAUGESOUND","devName":"Speaker Out","cardId":"hw:0,1","bitDepthSupportList":[16,24],"sampleRateSupportList":[44100,48000]}}]`,
		activeOutputCommand: `{"index":"1","mode":"AUDIO_OUTPUT_SPEAKER_MODE","cardId":"hw:0,1","devName":"Speaker Out","btSource":"0","audioCast":"0"}`,
		subCommand:          `{"status":1,"delay_main_sub":"1.0","plugged":1,"output_mode":1,"cross":80,"phase":0,"level":0,"mix_sub":1,"main_filter":1,"sub_filter":1,"sub_delay":-16}`,
		spdifCommand:        "800",
		playModeEnable:      "1",
		playModeCommand:     `[{"mode":10,"name":"wifi","volume":29,"stored":1,"active":1},{"mode":43,"name":"optical","volume":63,"stored":1,"active":0}]`,
		remoteStepCommand:   `{"status":"OK","remote_volume_step":1}`,
		buttonStepCommand:   `{"status":"OK","button_volume_step":2}`,
		cecCommand:          "1",
		powerModeCommand:    `{"idleInterval":"120","standbyInterval":"120","sleepInterval":"60","poweroffInterval":"0","semiactiveInterval":"15"}`,
		staticIPCommand:     `{"wlanStaticIp":"","wlanGateWay":"","wlanDnsServer":"","wlanStaticIpEnable":0,"ethStaticIp":"192.0.2.10/24","ethGateWay":"192.0.2.1","ethDnsServer":"192.0.2.53","ethStaticIpEnable":1}`,
		presetCommand:       `{"preset_num":1,"preset_list":[{"number":1,"name":"Radio","url":"http://x","source":"TuneIn","picurl":"http://p"}]}`,
		alarmCommand + ":0": `{"enable":"1","trigger":"2","operation":"1","time":"07:00:00","path":"http://x"}`,
		alarmCommand + ":1": `{"enable":"0"}`,
		alarmCommand + ":2": `{"enable":"0"}`,
		btPairCommand:       `{"result":3}`,
		btHistoryCommand:    `{"num":1,"scan_status":0,"list":[{"name":"Phone","ad":"00:00:00:00:00:01","role":"Audio Source","rssi":0}]}`,
		ledCommand:          "1",
		buttonsCommand:      "0",
	}}
	amp.server = httptest.NewTLSServer(http.HandlerFunc(amp.handle))
	t.Cleanup(amp.server.Close)
	return amp
}

func (f *fakeAmp) handle(w http.ResponseWriter, r *http.Request) {
	command := r.URL.Query().Get("command")
	f.mutex.Lock()
	f.commands = append(f.commands, command)
	answer, held := f.answers[command]
	failing := f.failing
	setterAnswer := f.setter
	f.mutex.Unlock()

	if failing != 0 {
		w.WriteHeader(failing)
		return
	}
	if isSetter(command) {
		if setterAnswer != "" {
			fmt.Fprint(w, setterAnswer)
			return
		}
		fmt.Fprint(w, "OK")
		return
	}
	if !held {
		fmt.Fprint(w, "unknown command")
		return
	}
	fmt.Fprint(w, answer)
}

func isSetter(command string) bool {
	for _, prefix := range []string{
		setVolumePrefix, setMutePrefix, setInputPrefix, setShutdownPrefix,
		"setChannelBalance", "setDeviceName", "LED_SWITCH_SET:", "Button_Enable_SET:",
		"MCUKeyShortClick:", "startbtdiscovery:", "connectbta2dpsynk:", "disconnectbta2dpsynk:",
		"reboot",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeAmp) client(listener func(equipment.Event)) *Client {
	return NewClient(f.server.Listener.Addr().String(), listener)
}

func (f *fakeAmp) sent() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string(nil), f.commands...)
}

// drainEvents empties the events a poll queued, so a test reads only
// what the next poll reports.
func drainEvents(events <-chan equipment.Event) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

// One poll fills every family of the snapshot.
func TestPollFoldsEveryFamily(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	if !client.poll(context.Background()) {
		t.Fatal("the poll did not answer")
	}
	status := client.Status()

	mustMatch(t, client.Address(), amp.server.Listener.Addr().String())
	mustMatch(t, status.Device.Name, "Test Amp")
	mustMatch(t, status.Device.Model, "WiiM_Amp_4layer")
	mustMatch(t, status.Device.UUID, "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, status.Device.MACs.Ethernet, "00:11:22:33:44:58")
	mustMatch(t, status.Device.Internet, true)
	mustMatch(t, status.Device.Capabilities.PresetKeys, 12)
	mustMatch(t, status.Device.Security.AES, "1.0")
	mustMatch(t, status.Device.TemperatureCPU, 32)

	mustMatch(t, status.Network.Interface, "ethernet")
	mustMatch(t, status.Network.Address, "192.0.2.10")
	mustMatch(t, status.Network.SSID, "Test")
	mustMatch(t, status.Network.Static.Ethernet.Enabled, true)
	mustMatch(t, status.Network.Static.Ethernet.Gateway, "192.0.2.1")

	mustMatch(t, status.Playback.Status, "play")
	mustMatch(t, status.Playback.Source, "optical")
	mustMatch(t, status.Playback.Volume, 31)
	mustMatch(t, status.Playback.Title, "Have a Cigar")
	mustMatch(t, status.Playback.Artist, "Pink Floyd")

	mustMatch(t, status.NowPlaying.Title, "Have a Cigar")
	mustMatch(t, status.NowPlaying.SampleRate, 44100)
	mustMatch(t, status.NowPlaying.BitDepth, 24)

	mustMatch(t, status.Audio.Balance, 0.5)
	mustMatch(t, status.Audio.MaxVolume, 100)
	mustMatch(t, status.Audio.Output.Mode, 7)
	mustMatch(t, status.Audio.Output.Name, "Speaker Out")
	mustMatch(t, status.Audio.Output.Available[0].SampleRates, []int{44100, 48000})
	mustMatch(t, status.Audio.RemoteVolumeStep, 1)
	mustMatch(t, status.Audio.ButtonVolumeStep, 2)
	mustMatch(t, status.Audio.PlayModeVolumes[1].Name, "optical")
	mustMatch(t, status.Audio.Subwoofer.Crossover, 80.0)
	mustMatch(t, status.Audio.SPDIFDelayMS, 800)

	mustMatch(t, status.Equalizer.Enabled, true)
	mustMatch(t, status.Equalizer.Presets, []string{"Flat", "Rock"})

	mustMatch(t, status.Timers.SleepSeconds, 300)
	mustMatch(t, status.Timers.Alarms[0].Enabled, true)
	mustMatch(t, status.Timers.PowerMode.Idle, 120)

	mustMatch(t, status.Bluetooth.PairStatus, 3)
	mustMatch(t, status.Bluetooth.Paired[0].Name, "Phone")
	mustMatch(t, status.Bluetooth.Remote.Connected, true)

	mustMatch(t, status.Presets[0].Source, "TuneIn")

	mustMatch(t, status.Controls.LED, true)
	mustMatch(t, status.Controls.Buttons, false)
	mustMatch(t, status.Controls.CECPower, true)
}

// The contract carries one main zone, power always on, and the volume
// the player reported.
func TestStateCarriesTheMainZone(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())

	state := client.State()
	mustMatch(t, state.Reachable, equipment.ConditionTrue)
	zone, held := state.Zone(equipment.MainZone)
	mustMatch(t, held, true)
	mustMatch(t, zone.Power, equipment.PowerOn)
	mustMatch(t, zone.Input, "optical")
	mustMatch(t, zone.Volume, 31)
	mustMatch(t, zone.VolumeMax, 100)
	mustMatch(t, zone.Sleep, 5)
	mustMatch(t, client.VolumeResolution(), 1)
}

func TestStateBeforeTheDeviceAnswers(t *testing.T) {
	client := NewClient("192.0.2.1", nil)

	state := client.State()
	mustMatch(t, state.Reachable, equipment.ConditionUnknown)
	zone, _ := state.Zone(equipment.MainZone)
	mustMatch(t, zone.Volume, equipment.Unknown)
	mustMatch(t, zone.VolumeMax, equipment.Unknown)
	mustMatch(t, zone.Sleep, equipment.Unknown)
}

// A device whose uuid does not match the declared identity is not
// driven, and reports unreachable.
func TestIdentityMismatchReportsUnreachable(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.UUID = "FF98F2F70000000000000000"

	for range pollFailures {
		if client.poll(context.Background()) {
			t.Fatal("a mismatched uuid answered as the expected device")
		}
	}
	mustMatch(t, client.State().Reachable, equipment.ConditionFalse)
	for _, command := range amp.sent() {
		if command != statusCommand {
			t.Fatalf("a mismatched device was sent %q", command)
		}
	}
}

func TestIdentityMatchesNormalizesTheSpellings(t *testing.T) {
	client := NewClient("192.0.2.1", nil)
	client.UUID = "FF98F2F7AABBCCDDEEFF0011"

	mustMatch(t, client.identityMatches("FF98F2F7AABBCCDDEEFF0011"), true)
	mustMatch(t, client.identityMatches("ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7"), true)
	mustMatch(t, client.identityMatches("FF98F2F70000000000000000"), false)
	mustMatch(t, client.identityMatches(""), false)

	client.UUID = ""
	mustMatch(t, client.identityMatches("anything"), true)
}

func TestAFailedPollReportsUnreachable(t *testing.T) {
	amp := startFakeAmp(t)
	amp.mutex.Lock()
	amp.failing = http.StatusInternalServerError
	amp.mutex.Unlock()
	client := amp.client(nil)

	for range pollFailures {
		if client.poll(context.Background()) {
			t.Fatal("a failing device answered as reachable")
		}
	}
	mustMatch(t, client.State().Reachable, equipment.ConditionFalse)
}

// One poll the device misses leaves the last answer standing, and the
// polls after it call the device unreachable.
func TestOneMissedPollLeavesTheDeviceReachable(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())
	mustMatch(t, client.State().Reachable, equipment.ConditionTrue)

	amp.mutex.Lock()
	amp.failing = http.StatusInternalServerError
	amp.mutex.Unlock()
	client.poll(context.Background())
	mustMatch(t, client.State().Reachable, equipment.ConditionTrue)

	for i := 1; i < pollFailures; i++ {
		client.poll(context.Background())
	}
	mustMatch(t, client.State().Reachable, equipment.ConditionFalse)
}

// A poll the device answers clears the run of misses, so a device that
// stumbles once does not go unreachable on a later run counted from the
// stumble.
func TestAnAnsweredPollClearsTheMisses(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	amp.mutex.Lock()
	amp.failing = http.StatusInternalServerError
	amp.mutex.Unlock()
	for i := 0; i < pollFailures-1; i++ {
		client.poll(context.Background())
	}

	amp.mutex.Lock()
	amp.failing = 0
	amp.mutex.Unlock()
	if !client.poll(context.Background()) {
		t.Fatal("the device answered and the poll reported failure")
	}
	mustMatch(t, client.State().Reachable, equipment.ConditionTrue)

	amp.mutex.Lock()
	amp.failing = http.StatusInternalServerError
	amp.mutex.Unlock()
	for i := 0; i < pollFailures-1; i++ {
		client.poll(context.Background())
	}
	mustMatch(t, client.State().Reachable, equipment.ConditionTrue)
}

// A source with no track answers "Failed", and the track is cleared.
func TestAMissOnNowPlayingClearsTheTrack(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())
	if client.Status().NowPlaying == nil {
		t.Fatal("the first poll did not read a track")
	}

	amp.mutex.Lock()
	amp.answers[metaCommand] = "Failed"
	amp.mutex.Unlock()
	client.poll(context.Background())
	mustMatch(t, client.Status().NowPlaying, (*NowPlaying)(nil))
}

// A command the device does not carry leaves its family as it was.
func TestAnUnknownCommandLeavesItsFamily(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())

	amp.mutex.Lock()
	delete(amp.answers, eqListCommand)
	amp.mutex.Unlock()
	client.poll(context.Background())
	mustMatch(t, client.Status().Equalizer.Presets, []string{"Flat", "Rock"})
}

// A poll that moves nothing still pokes the listener, so the status
// writer records the observation.
func TestAPollThatMovedNothingStillReports(t *testing.T) {
	amp := startFakeAmp(t)
	events := make(chan equipment.Event, 16)
	client := amp.client(func(event equipment.Event) { events <- event })

	client.poll(context.Background())
	<-events
	client.poll(context.Background())
	select {
	case event := <-events:
		mustMatch(t, event.Zone, equipment.MainZone)
	case <-time.After(time.Second):
		t.Fatal("a quiet poll reported nothing")
	}
}

// A hand on the device moves the volume, and the listener hears it as a
// volume event rather than a reachability change.
func TestAChangedVolumeReachesTheListener(t *testing.T) {
	amp := startFakeAmp(t)
	events := make(chan equipment.Event, 16)
	client := amp.client(func(event equipment.Event) { events <- event })
	client.poll(context.Background())
	drainEvents(events)

	amp.mutex.Lock()
	amp.answers[playerCommand] = strings.Replace(playerJSON, `"vol":"31"`, `"vol":"40"`, 1)
	amp.mutex.Unlock()
	client.poll(context.Background())

	select {
	case event := <-events:
		mustMatch(t, event.Field, equipment.EventVolume)
	case <-time.After(time.Second):
		t.Fatal("a changed volume reported nothing")
	}
}

// A setter takes the level it just sent, so a press moves the room
// before the next poll and two presses in one poll window each count.
func TestASetterTakesTheLevelItSent(t *testing.T) {
	amp := startFakeAmp(t)
	events := make(chan equipment.Event, 16)
	client := amp.client(func(event equipment.Event) { events <- event })
	client.poll(context.Background())
	drainEvents(events)

	if err := client.SetVolume(equipment.MainZone, 42); err != nil {
		t.Fatal(err)
	}
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, 42)
	select {
	case event := <-events:
		mustMatch(t, event.Field, equipment.EventVolume)
	case <-time.After(time.Second):
		t.Fatal("a taken volume reported nothing")
	}

	if err := client.SetMute(equipment.MainZone, true); err != nil {
		t.Fatal(err)
	}
	mustMatch(t, client.State().Zones[equipment.MainZone].Mute, true)
}

// A setter the device refuses takes nothing, so the held state never
// claims a level that did not land.
func TestARefusedSetterTakesNothing(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	client.poll(context.Background())
	before := client.State().Zones[equipment.MainZone].Volume

	amp.mutex.Lock()
	amp.setter = "unknown command"
	amp.mutex.Unlock()
	if err := client.SetVolume(equipment.MainZone, 42); err == nil {
		t.Fatal("the device refused the set and the client reported no error")
	}
	mustMatch(t, client.State().Zones[equipment.MainZone].Volume, before)
}

// Run keeps polling until its context ends.
func TestRunPollsUntilTheContextEnds(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)
	restore := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = restore })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		client.Run(ctx)
		close(done)
	}()
	deadline := time.After(3 * time.Second)
	for client.State().Reachable != equipment.ConditionTrue {
		select {
		case <-deadline:
			t.Fatal("Run never reached the device")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

// Each setter reaches its own wire command.
func TestTheSettersReachTheirWireCommands(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		send func() error
		want string
	}{
		{"volume", func() error { return client.SetVolume(equipment.MainZone, 42) }, "setPlayerCmd:vol:42"},
		{"volume over the top", func() error { return client.SetVolume(equipment.MainZone, 500) }, "setPlayerCmd:vol:100"},
		{"volume under zero", func() error { return client.SetVolume(equipment.MainZone, -5) }, "setPlayerCmd:vol:0"},
		{"mute on", func() error { return client.SetMute(equipment.MainZone, true) }, "setPlayerCmd:mute:1"},
		{"mute off", func() error { return client.SetMute(equipment.MainZone, false) }, "setPlayerCmd:mute:0"},
		{"input", func() error { return client.SetInput(equipment.MainZone, "optical") }, "setPlayerCmd:switchmode:optical"},
		{"sleep", func() error { return client.SetSleep(equipment.MainZone, 10) }, "setShutdown:600"},
		{"sleep off", func() error { return client.SetSleep(equipment.MainZone, 0) }, "setShutdown:-1"},
		{"power on is a no-op", func() error { return client.SetPower(equipment.MainZone, true) }, ""},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			amp.mutex.Lock()
			amp.commands = nil
			amp.mutex.Unlock()
			mustSucceed(t, one.send())
			if one.want == "" {
				mustMatch(t, amp.sent(), []string(nil))
				return
			}
			mustMatch(t, amp.sent(), []string{one.want})
		})
	}
}

// The setters reject what the device cannot do: an unknown zone, a
// power-off, a sound mode, an unknown input, and an out-of-range timer.
func TestTheSettersRejectWhatTheDeviceCannotDo(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	cases := []struct {
		name string
		send func() error
	}{
		{"power off", func() error { return client.SetPower(equipment.MainZone, false) }},
		{"sound mode", func() error { return client.SetSoundMode(equipment.MainZone, "STEREO") }},
		{"unknown input", func() error { return client.SetInput(equipment.MainZone, "coaxial") }},
		{"sleep over the top", func() error { return client.SetSleep(equipment.MainZone, maxSleepMinutes+1) }},
		{"sleep under zero", func() error { return client.SetSleep(equipment.MainZone, -1) }},
		{"power on an unknown zone", func() error { return client.SetPower("zone2", true) }},
		{"input on an unknown zone", func() error { return client.SetInput("zone2", "optical") }},
		{"volume on an unknown zone", func() error { return client.SetVolume("zone2", 1) }},
		{"mute on an unknown zone", func() error { return client.SetMute("zone2", true) }},
		{"sleep on an unknown zone", func() error { return client.SetSleep("zone2", 1) }},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if err := one.send(); err == nil {
				t.Fatalf("%s did not error", one.name)
			}
		})
	}
}

func TestInputForMode(t *testing.T) {
	cases := []struct {
		mode int
		want string
	}{
		{0, ""}, {1, "wifi"}, {10, "wifi"}, {11, "udisk"}, {16, "udisk"},
		{31, "wifi"}, {40, "line-in"}, {41, "bluetooth"}, {43, "optical"},
		{47, "line-in"}, {49, "hdmi"}, {51, "usb"}, {99, "wifi"},
	}
	for _, one := range cases {
		mustMatch(t, inputForMode(one.mode), one.want)
	}
}

func TestNormalizeUUID(t *testing.T) {
	mustMatch(t, normalizeUUID("ff98f2f7-aabb-ccdd-eeff-0011ff98f2f7"), "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, normalizeUUID("FF98F2F7AABBCCDDEEFF0011"), "FF98F2F7AABBCCDDEEFF0011")
	mustMatch(t, normalizeUUID(""), "")
}

func TestDecodeHex(t *testing.T) {
	mustMatch(t, decodeHex("48656C6C6F"), "Hello")
	mustMatch(t, decodeHex(""), "")
	mustMatch(t, decodeHex("zz"), "")
}

func TestSleepMinutes(t *testing.T) {
	mustMatch(t, sleepMinutes(-1), equipment.Unknown)
	mustMatch(t, sleepMinutes(0), 0)
	mustMatch(t, sleepMinutes(60), 1)
	mustMatch(t, sleepMinutes(61), 2)
}

func TestCommandURLFillsThePort(t *testing.T) {
	mustMatch(t, commandURL("192.0.2.1", "getStatusEx"), "https://192.0.2.1:443/httpapi.asp?command=getStatusEx")
	mustMatch(t, commandURL("192.0.2.1:8443", "getStatusEx"), "https://192.0.2.1:8443/httpapi.asp?command=getStatusEx")
}

// A field the device sends as a number on one firmware and a string on
// another reads either way.
func TestFlexStringReadsEitherShape(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"number", `1`, "1"},
		{"string", `"1"`, "1"},
		{"zero", `0`, "0"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			var value flexString
			mustSucceed(t, value.UnmarshalJSON([]byte(one.body)))
			mustMatch(t, string(value), one.want)
		})
	}
}

func TestModeForInput(t *testing.T) {
	for _, input := range []string{"line-in", "bluetooth", "optical", "hdmi", "usb", "udisk", "wifi"} {
		mode, ok := modeForInput(input)
		mustMatch(t, ok, true)
		mustMatch(t, mode, input)
	}
	if _, ok := modeForInput("coaxial"); ok {
		t.Fatal("an unknown input mapped")
	}
}

func TestAtoiAndAtofRejectNonNumbers(t *testing.T) {
	if _, ok := atoi("x"); ok {
		t.Fatal("atoi accepted a word")
	}
	if _, ok := atof("x"); ok {
		t.Fatal("atof accepted a word")
	}
	mustMatch(t, intOr("x", 7), 7)
}

// A setter the device refuses carries the device's own words.
func TestASetterFailureCarriesTheDeviceWords(t *testing.T) {
	amp := startFakeAmp(t)
	amp.mutex.Lock()
	amp.setter = "unknown command"
	amp.mutex.Unlock()
	client := amp.client(nil)

	err := client.SetVolume(equipment.MainZone, 10)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("a refused setter did not carry the device's words: %v", err)
	}
}

// Run backs off while the device is down, and reports it unreachable.
func TestRunBacksOffWhenTheDeviceIsDown(t *testing.T) {
	restore := pollInterval
	restoreMin, restoreMax := minBackoff, maxBackoff
	t.Cleanup(func() { pollInterval, minBackoff, maxBackoff = restore, restoreMin, restoreMax })
	minBackoff, maxBackoff = time.Millisecond, 5*time.Millisecond

	client := NewClient("127.0.0.1:1", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	client.Run(ctx)

	mustMatch(t, client.State().Reachable, equipment.ConditionFalse)
}

// The survey is not complete until one poll has read every family, so
// the controller knows the device's own facts are in hand before it
// applies a declared setting.
func TestASurveyCompletesAfterOnePoll(t *testing.T) {
	amp := startFakeAmp(t)
	client := amp.client(nil)

	mustMatch(t, client.Surveyed(), false)
	client.poll(context.Background())
	mustMatch(t, client.Surveyed(), true)
}
