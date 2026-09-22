// The client that reaches one WiiM over its stateless HTTPS API. There
// is no connection to hold open: Run polls, folds each answer into the
// device's own state, and translates between that state and the
// equipment.Driver contract. A setter is its own GET.

package wiim

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// These bound one request and the wait between polls. A failed poll
// backs off, because a device that is down should not be hammered.
var (
	requestTimeout = 8 * time.Second
	pollInterval   = 10 * time.Second
	minBackoff     = time.Second
	maxBackoff     = 30 * time.Second
)

// maxSleepMinutes bounds the sleep timer the driver accepts.
const maxSleepMinutes = 1440

// volumeStepMax is the top of the device's own scale, which reads 0 to
// 100 in whole steps.
const volumeStepMax = 100

// Client holds one device's address and the state it last reported. The
// API is unauthenticated HTTPS with a self-signed certificate, so the
// transport skips verification.
type Client struct {
	address  string
	listener func(equipment.Event)

	// Reporter counts the outcome of every command this client sends. A
	// nil Reporter is a no-op.
	Reporter func(status string)

	// UUID is the identity the device must report before the client
	// drives it. An empty value skips the check.
	UUID string

	http *http.Client

	mutex     sync.Mutex
	state     Status
	reachable equipment.ConditionStatus
}

// NewClient builds a client for one address. The listener reports the
// state after every poll. The address is a hint; set UUID to hold the
// client to the device's identity.
func NewClient(address string, listener func(equipment.Event)) *Client {
	return &Client{
		address:  address,
		listener: listener,
		http: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		state:     newStatus(),
		reachable: equipment.ConditionUnknown,
	}
}

// State returns the latest device state translated into equipment
// units, and can run concurrently with Run.
func (c *Client) State() equipment.State {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.state.equipmentState(c.reachable)
}

// Status returns the device's own typed snapshot, which the controller
// writes under status.wiim.
func (c *Client) Status() Status {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.state
}

// VolumeResolution is the number of the device's steps in one display
// unit: the wire counts whole steps from 0 to 100, so it is 1.
func (c *Client) VolumeResolution() int { return 1 }

// Run polls until ctx ends, backing off after a poll the device did not
// answer. Reachability flips false on a failed poll and true on the
// next answered one.
func (c *Client) Run(ctx context.Context) {
	backoff := minBackoff
	for ctx.Err() == nil {
		answered := c.poll(ctx)
		wait := pollInterval
		if answered {
			backoff = minBackoff
		} else {
			wait = backoff
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// poll reads the whole command set once. getStatusEx comes first,
// because it carries the identity: if the device is not the one the
// spec declares, the client folds nothing and reports unreachable
// rather than drive the wrong amp. Every other read is best effort, so
// a command one model does not carry leaves its family as it was.
func (c *Client) poll(ctx context.Context) bool {
	c.mutex.Lock()
	next := c.state
	c.mutex.Unlock()

	if err := c.readStatusEx(ctx, &next); err != nil {
		c.report(CommandFailed)
		c.record(equipment.ConditionFalse)
		return false
	}
	if !c.identityMatches(next.Device.UUID) {
		c.report(CommandFailed)
		c.record(equipment.ConditionFalse)
		return false
	}
	c.report(CommandOK)

	c.readStaticIP(ctx, &next)
	c.readPlayback(ctx, &next)
	c.readNowPlaying(ctx, &next)
	c.readAudio(ctx, &next)
	c.readEqualizer(ctx, &next)
	c.readTimers(ctx, &next)
	c.readBluetooth(ctx, &next)
	c.readPresets(ctx, &next)
	c.readControls(ctx, &next)

	c.publish(next)
	return true
}

// identityMatches answers whether the device's reported uuid is the one
// the spec declared. An empty expected value skips the check; a device
// that reported no uuid cannot be confirmed.
func (c *Client) identityMatches(reported string) bool {
	if c.UUID == "" {
		return true
	}
	if reported == "" {
		return false
	}
	return normalizeUUID(c.UUID) == normalizeUUID(reported)
}

// publish folds the freshly read status in, sets reachability, and
// tells the listener which fields a hand on the device moved. A poll
// that moved nothing still reports, so the status writer records the
// observation while the receiver stands still.
func (c *Client) publish(next Status) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	c.state = next
	c.reachable = equipment.ConditionTrue
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// takeVolume folds the level the device was just told into the held
// state, and takeMute folds the mute the same way. A poll reaches the
// device at most once every interval, so without this a press computes
// against a reading up to one interval old and two presses in that
// window both move the room from the same place. The next poll carries
// the device's own report and folds it as usual. A command the device
// refuses returns before here, so a take never states a level that did
// not land.
func (c *Client) takeVolume(volume int) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	c.state.Playback.Volume = volume
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

func (c *Client) takeMute(muted bool) {
	c.mutex.Lock()
	previous := c.state
	wasReachable := c.reachable
	c.state.Playback.Mute = muted
	next := c.state
	c.mutex.Unlock()
	c.announce(previous, next, wasReachable)
}

// announce reports what a state change moved, from the fields the
// equipment contract names. A poll and a command that takes the level
// it just sent both run through here, so a press looks the same to the
// session as a hand on the device.
func (c *Client) announce(previous, next Status, wasReachable equipment.ConditionStatus) {
	state := next.equipmentState(equipment.ConditionTrue)
	before := previous.equipmentState(equipment.ConditionTrue)
	beforeZone := before.Zones[equipment.MainZone]
	afterZone := state.Zones[equipment.MainZone]
	switch {
	case beforeZone.Volume != afterZone.Volume:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventVolume, State: state})
	case beforeZone.Mute != afterZone.Mute:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventMute, State: state})
	case beforeZone.Input != afterZone.Input:
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventInput, State: state})
	}
	if wasReachable != equipment.ConditionTrue {
		c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventReachable, State: state})
		return
	}
	c.notify(equipment.Event{Zone: equipment.MainZone, State: state})
}

// record changes the reachability verdict and notifies the listener.
func (c *Client) record(status equipment.ConditionStatus) {
	c.mutex.Lock()
	if c.reachable == status {
		c.mutex.Unlock()
		return
	}
	c.reachable = status
	state := c.state.equipmentState(status)
	c.mutex.Unlock()
	c.notify(equipment.Event{Zone: equipment.MainZone, Field: equipment.EventReachable, State: state})
}

// report tells the wired Reporter about one command's outcome.
func (c *Client) report(status string) {
	if c.Reporter != nil {
		c.Reporter(status)
	}
}

// notify runs on the polling goroutine, so a blocking listener delays
// the wait before the next poll and nothing else.
func (c *Client) notify(event equipment.Event) {
	if c.listener != nil {
		c.listener(event)
	}
}

// call performs one GET and answers the body. A non-2xx answer carries
// the server's own text in the error.
func (c *Client) call(ctx context.Context, command string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, commandURL(c.address, command), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("%s answered %s: %s", command, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// read calls one command and hands the body to parse. A command the
// device does not carry, or a read it cannot serve, is a miss and not
// an error: the family keeps its last value.
func (c *Client) read(ctx context.Context, command string, parse func([]byte)) {
	body, err := c.call(ctx, command)
	if err != nil {
		return
	}
	if isMiss(body) {
		return
	}
	parse(body)
}

// isMiss answers whether a body is one of the device's words for a
// command it does not carry or a read it cannot serve.
func isMiss(body []byte) bool {
	text := strings.TrimSpace(string(body))
	return text == "" || text == "unknown command" || text == "Failed"
}

// readStatusEx reads the identity and hardware block, which is the one
// read a poll cannot miss.
func (c *Client) readStatusEx(ctx context.Context, next *Status) error {
	body, err := c.call(ctx, statusCommand)
	if err != nil {
		return err
	}
	var device statusEx
	if err := json.Unmarshal(body, &device); err != nil {
		return fmt.Errorf("%s answered %s: %w", statusCommand, strings.TrimSpace(string(body)), err)
	}
	applyStatusEx(next, device)
	return nil
}

// readStaticIP reads the static configuration the device holds.
func (c *Client) readStaticIP(ctx context.Context, next *Status) {
	c.read(ctx, staticIPCommand, func(body []byte) {
		var info staticIPInfo
		if json.Unmarshal(body, &info) != nil {
			return
		}
		next.Network.Static = &StaticIP{
			WiFi: StaticAddress{
				Address: info.WlanStaticIP, Gateway: info.WlanGateWay,
				DNS: info.WlanDNSServer, Enabled: info.WlanStaticIPEnable == 1,
			},
			Ethernet: StaticAddress{
				Address: info.EthStaticIP, Gateway: info.EthGateWay,
				DNS: info.EthDNSServer, Enabled: info.EthStaticIPEnable == 1,
			},
		}
	})
}

// readPlayback reads the player's own report.
func (c *Client) readPlayback(ctx context.Context, next *Status) {
	c.read(ctx, playerCommand, func(body []byte) {
		var player playerStatus
		if json.Unmarshal(body, &player) != nil {
			return
		}
		applyPlayback(next, player)
	})
}

// readNowPlaying reads the current track as plain JSON. A source with
// no track answers "Failed", and the track is cleared rather than left
// stale.
func (c *Client) readNowPlaying(ctx context.Context, next *Status) {
	body, err := c.call(ctx, metaCommand)
	if err != nil || isMiss(body) {
		next.NowPlaying = nil
		return
	}
	var meta metaInfo
	if json.Unmarshal(body, &meta) != nil {
		next.NowPlaying = nil
		return
	}
	next.NowPlaying = &NowPlaying{
		Title:      meta.MetaData.Title,
		Subtitle:   meta.MetaData.Subtitle,
		Artist:     meta.MetaData.Artist,
		Album:      meta.MetaData.Album,
		ArtURI:     meta.MetaData.AlbumArtURI,
		SampleRate: intOr(meta.MetaData.SampleRate, 0),
		BitDepth:   intOr(meta.MetaData.BitDepth, 0),
		BitRate:    intOr(meta.MetaData.BitRate, 0),
		TrackID:    meta.MetaData.TrackID,
	}
}

// readAudio reads the output block and the level controls.
func (c *Client) readAudio(ctx context.Context, next *Status) {
	c.read(ctx, outputModeCommand, func(body []byte) {
		var mode outputMode
		if json.Unmarshal(body, &mode) != nil {
			return
		}
		next.Audio.Output.Hardware = mode.Hardware
		next.Audio.Output.Mode = intOr(mode.Hardware, 0)
		next.Audio.Output.Source = mode.Source == "1"
		next.Audio.Output.AudioCast = mode.AudioCast == "1"
	})
	c.read(ctx, outputListCommand, func(body []byte) {
		var list []outputModeListEntry
		if json.Unmarshal(body, &list) != nil {
			return
		}
		available := make([]OutputMode, 0, len(list))
		for _, entry := range list {
			available = append(available, OutputMode{
				Index:       entry.Index,
				Mode:        entry.Mode,
				Device:      entry.SoundCard.DeviceName,
				CardID:      entry.SoundCard.CardID,
				BitDepths:   entry.SoundCard.BitDepths,
				SampleRates: entry.SoundCard.SampleRates,
			})
		}
		next.Audio.Output.Available = available
	})
	c.read(ctx, activeOutputCommand, func(body []byte) {
		var active activeOutput
		if json.Unmarshal(body, &active) != nil {
			return
		}
		next.Audio.Output.Name = active.DeviceName
		next.Audio.Output.CardID = active.CardID
	})
	c.read(ctx, balanceCommand, func(body []byte) {
		if value, ok := atof(string(body)); ok {
			next.Audio.Balance = value
		}
	})
	c.read(ctx, subCommand, func(body []byte) {
		var sub subwoofer
		if json.Unmarshal(body, &sub) != nil {
			return
		}
		next.Audio.Subwoofer = &Subwoofer{
			Plugged:    sub.Plugged == 1,
			OutputMode: sub.OutputMode,
			Crossover:  sub.Cross,
			Phase:      sub.Phase,
			Level:      sub.Level,
			Delay:      sub.SubDelay,
			MainFilter: sub.MainFilter == 1,
			SubFilter:  sub.SubFilter == 1,
			MixSub:     sub.MixSub == 1,
		}
	})
	c.read(ctx, spdifCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Audio.SPDIFDelayMS = value
		}
	})
	c.read(ctx, remoteStepCommand, func(body []byte) {
		var steps stepAnswer
		if json.Unmarshal(body, &steps) == nil {
			next.Audio.RemoteVolumeStep = steps.RemoteVolumeStep
		}
	})
	c.read(ctx, buttonStepCommand, func(body []byte) {
		var steps stepAnswer
		if json.Unmarshal(body, &steps) == nil {
			next.Audio.ButtonVolumeStep = steps.ButtonVolumeStep
		}
	})
	c.read(ctx, playModeEnable, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Audio.PlayModeVolumeEnabled = value == 1
		}
	})
	c.read(ctx, playModeCommand, func(body []byte) {
		var modes []perInputVolume
		if json.Unmarshal(body, &modes) != nil {
			return
		}
		volumes := make([]InputVolume, 0, len(modes))
		for _, mode := range modes {
			volumes = append(volumes, InputVolume{
				Mode:   mode.Mode,
				Name:   mode.Name,
				Volume: mode.Volume,
				Stored: mode.Stored == 1,
				Active: mode.Active == 1,
			})
		}
		next.Audio.PlayModeVolumes = volumes
	})
}

// readEqualizer reads the equalizer's state and its presets.
func (c *Client) readEqualizer(ctx context.Context, next *Status) {
	c.read(ctx, eqStatCommand, func(body []byte) {
		var answer eqAnswer
		if json.Unmarshal(body, &answer) == nil && answer.EQStat != "" {
			next.Equalizer.Enabled = answer.EQStat == "On"
		}
	})
	c.read(ctx, eqListCommand, func(body []byte) {
		var presets []string
		if json.Unmarshal(body, &presets) == nil {
			next.Equalizer.Presets = presets
		}
	})
}

// readTimers reads the sleep timer, the idle intervals, and the three
// alarm slots.
func (c *Client) readTimers(ctx context.Context, next *Status) {
	c.read(ctx, shutdownCommand, func(body []byte) {
		if seconds, ok := atoi(string(body)); ok {
			next.Timers.SleepSeconds = seconds
		}
	})
	c.read(ctx, powerModeCommand, func(body []byte) {
		var mode powerModeTime
		if json.Unmarshal(body, &mode) != nil {
			return
		}
		next.Timers.PowerMode = PowerMode{
			Idle:       intOr(mode.IdleInterval, 0),
			Standby:    intOr(mode.StandbyInterval, 0),
			Sleep:      intOr(mode.SleepInterval, 0),
			PowerOff:   intOr(mode.PowerOffInterval, 0),
			SemiActive: intOr(mode.SemiActiveInterval, 0),
		}
	})
	alarms := make([]Alarm, 0, 3)
	for index := 0; index < 3; index++ {
		slot := index
		c.read(ctx, alarmCommand+":"+strconv.Itoa(slot), func(body []byte) {
			var alarm alarmClock
			if json.Unmarshal(body, &alarm) != nil || alarm.Enable == "" {
				return
			}
			alarms = append(alarms, Alarm{
				Index:     slot,
				Enabled:   alarm.Enable == "1",
				Trigger:   intOr(alarm.Trigger, 0),
				Operation: intOr(alarm.Operation, 0),
				Time:      alarm.Time,
				Day:       alarm.Day,
				Path:      alarm.Path,
			})
		})
	}
	next.Timers.Alarms = alarms
}

// readBluetooth reads the pairing state and the paired devices.
func (c *Client) readBluetooth(ctx context.Context, next *Status) {
	c.read(ctx, btPairCommand, func(body []byte) {
		var pair btPairStatus
		if json.Unmarshal(body, &pair) == nil {
			next.Bluetooth.PairStatus = pair.Result
		}
	})
	c.read(ctx, btHistoryCommand, func(body []byte) {
		var history btHistory
		if json.Unmarshal(body, &history) != nil {
			return
		}
		paired := make([]PairedDevice, 0, len(history.List))
		for _, device := range history.List {
			paired = append(paired, PairedDevice{Name: device.Name, Address: device.Address, Role: device.Role})
		}
		next.Bluetooth.Paired = paired
	})
}

// readPresets reads the stored presets.
func (c *Client) readPresets(ctx context.Context, next *Status) {
	c.read(ctx, presetCommand, func(body []byte) {
		var info presetInfo
		if json.Unmarshal(body, &info) != nil {
			return
		}
		presets := make([]Preset, 0, len(info.PresetList))
		for _, preset := range info.PresetList {
			presets = append(presets, Preset{Number: preset.Number, Name: preset.Name, Source: preset.Source})
		}
		next.Presets = presets
	})
}

// readControls reads the status light, the touch buttons, and CEC.
func (c *Client) readControls(ctx context.Context, next *Status) {
	c.read(ctx, ledCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.LED = value == 1
		}
	})
	c.read(ctx, buttonsCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.Buttons = value == 1
		}
	})
	c.read(ctx, cecCommand, func(body []byte) {
		if value, ok := atoi(string(body)); ok {
			next.Controls.CECPower = value == 1
		}
	})
}

// applyStatusEx folds the identity and hardware block into the
// snapshot.
func applyStatusEx(s *Status, d statusEx) {
	s.Device.Name = d.DeviceName
	s.Device.Group = d.GroupName
	s.Device.Model = d.Project
	s.Device.Firmware = d.Firmware
	s.Device.Build = d.Build
	s.Device.Released = d.Release
	s.Device.Hardware = d.Hardware
	s.Device.BoardVersion = d.PCBVersion
	s.Device.Uboot = d.UbootVer
	s.Device.MCU = d.MCUVer
	s.Device.HDMI = d.HDMIVer
	s.Device.UUID = normalizeUUID(d.UUID)
	s.Device.TempUUID = d.TempUUID
	s.Device.UPnPUUID = d.UPnPUUID
	s.Device.MACs = MACs{WiFi: d.MAC, Bluetooth: d.BTMAC, AccessPoint: d.APMAC, Ethernet: d.ETHMAC}
	s.Device.Internet = d.Internet == "1"
	s.Device.Initialized = d.InitialConfiguration == "1"
	s.Device.PrivacyMode = d.PrivacyMode == "1"
	s.Device.Language = d.Language
	s.Device.Timezone = d.TimezoneID
	s.Device.TimezoneOffset = d.Timezone
	s.Device.Date = d.Date
	s.Device.Time = d.Time
	s.Device.CommunicationPort = d.CommunicationPort
	s.Device.TemperatureCPU = intOr(d.TemperatureCPU, 0)
	s.Device.TemperatureBoard = intOr(d.TemperatureTmp102, 0)
	s.Device.Capabilities = Capabilities{
		Cast:          d.CastEnable == "1",
		PresetKeys:    intOr(d.PresetKey, 0),
		MQTT:          d.MQTTSupport == "1",
		AudioCast:     d.AudioCast == "1",
		EQSupport:     d.EQSupport,
		EQVersion:     d.EQVersion,
		MaxVolume:     intOr(d.MaxVolume, 0),
		VolumeControl: intOr(d.VolumeControl, 0),
	}
	s.Device.Security = Security{
		Protocol: d.Security,
		Version:  d.SecurityVersion,
		AES:      d.SecurityCapabilities.AESVer,
	}
	if d.Eth0 != "" && d.Eth0 != "0.0.0.0" {
		s.Network.Interface = "ethernet"
		s.Network.Address = d.Eth0
	} else if d.Apcli0 != "" && d.Apcli0 != "0.0.0.0" {
		s.Network.Interface = "wifi"
		s.Network.Address = d.Apcli0
	}
	s.Network.SSID = decodeHex(d.Essid)
	s.Network.BSSID = d.BSSID
	s.Network.RSSI = intOr(d.RSSI, 0)
	s.Network.Noise = intOr(d.WlanNoise, 0)
	s.Network.SNR = intOr(d.WlanSnr, 0)
	s.Network.Channel = intOr(d.WifiChannel, 0)
	s.Network.Frequency = intOr(d.WlanFreq, 0)
	s.Network.DataRate = intOr(d.WlanDataRate, 0)
	s.Audio.MaxVolume = intOr(d.MaxVolume, equipment.Unknown)
	s.Bluetooth.Remote = &BLERemote{
		Supported: d.BleRemoteControl == "1",
		Connected: d.BleRemoteConnected == "1",
		Battery:   intOr(d.BleRemoteBattery, 0),
		RSSI:      intOr(d.BleRemoteRSSI, 0),
		Version:   d.BleRemoteVersion,
		State:     d.BleRemoteState,
	}
}

// applyPlayback folds the player's report into the snapshot, decoding
// the hex-encoded text the player carries.
func applyPlayback(s *Status, p playerStatus) {
	mode, _ := atoi(p.Mode)
	s.Playback = Playback{
		Status:        p.Status,
		Mode:          mode,
		Source:        inputForMode(mode),
		Loop:          intOr(p.Loop, 0),
		EQPreset:      intOr(p.EQ, 0),
		Vendor:        p.Vendor,
		Channel:       intOr(p.Channel, 0),
		Type:          intOr(p.Type, 0),
		Volume:        intOr(p.Volume, equipment.Unknown),
		Mute:          p.Mute == "1",
		PositionMS:    intOr(p.CurrentPosition, 0),
		OffsetMS:      intOr(p.Offset, 0),
		LengthMS:      intOr(p.TotalLength, 0),
		AlarmFlag:     p.AlarmFlag == "1",
		PlaylistCount: intOr(p.PlaylistCount, 0),
		PlaylistIndex: intOr(p.PlaylistIndex, 0),
		Title:         decodeHex(p.Title),
		Artist:        decodeHex(p.Artist),
		Album:         decodeHex(p.Album),
	}
	s.Equalizer.Preset = s.Playback.EQPreset
}

// intOr reads a number the device sent as a string, and answers the
// fallback when it is not one.
func intOr(value string, fallback int) int {
	if number, ok := atoi(value); ok {
		return number
	}
	return fallback
}

// SetPower answers that a WiiM has no standby: the API carries no power
// command, so on is a no-op and off is an error. Starting and stopping
// playback belongs to the media session's transport, not to the
// receiver's power.
func (c *Client) SetPower(zone string, on bool) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if on {
		return nil
	}
	return fmt.Errorf("a WiiM has no standby command; power off is not supported")
}

// SetInput selects one input by mapping its name onto the device's own
// switchmode value.
func (c *Client) SetInput(zone, input string) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	mode, ok := modeForInput(input)
	if !ok {
		return fmt.Errorf("unknown input %q", input)
	}
	return c.send(context.Background(), setInputPrefix+mode)
}

// SetVolume sets the volume, in whole steps. A count outside the
// device's 0 to 100 scale is held to it rather than refused.
func (c *Client) SetVolume(zone string, steps int) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if steps < 0 {
		steps = 0
	}
	if steps > volumeStepMax {
		steps = volumeStepMax
	}
	if err := c.send(context.Background(), setVolumePrefix+strconv.Itoa(steps)); err != nil {
		return err
	}
	c.takeVolume(steps)
	return nil
}

// SetMute sets the mute.
func (c *Client) SetMute(zone string, muted bool) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	value := "0"
	if muted {
		value = "1"
	}
	if err := c.send(context.Background(), setMutePrefix+value); err != nil {
		return err
	}
	c.takeMute(muted)
	return nil
}

// SetSoundMode answers that the protocol carries no sound mode.
func (c *Client) SetSoundMode(zone, mode string) error {
	return fmt.Errorf("a WiiM carries no sound mode")
}

// SetSleep sets the sleep timer in minutes. Zero cancels it, and a
// positive value pauses playback when the timer reaches zero. The
// device's own timer is the closest thing it has to power control.
func (c *Client) SetSleep(zone string, minutes int) error {
	if zone != equipment.MainZone {
		return unknownZone(zone)
	}
	if minutes < 0 || minutes > maxSleepMinutes {
		return fmt.Errorf("sleep %d minutes", minutes)
	}
	if minutes == 0 {
		return c.send(context.Background(), setShutdownPrefix+"-1")
	}
	return c.send(context.Background(), setShutdownPrefix+strconv.Itoa(minutes*60))
}

// unknownZone is the error a setter answers when a caller names a zone
// the device does not carry.
func unknownZone(zone string) error {
	return fmt.Errorf("unknown zone %q", zone)
}

// send performs one setter GET. The device answers OK, and anything
// else is an error carrying the device's own words.
func (c *Client) send(ctx context.Context, command string) error {
	body, err := c.call(ctx, command)
	if err != nil {
		c.report(CommandFailed)
		return err
	}
	if isMiss(body) {
		c.report(CommandFailed)
		return fmt.Errorf("%s answered %s", command, strings.TrimSpace(string(body)))
	}
	c.report(CommandOK)
	return nil
}
