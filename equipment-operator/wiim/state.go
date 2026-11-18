// Status is what a WiiM amp reports, in the device's own terms. The
// equipment contract is the smaller set of power, input, mute, and
// volume that every Receiver has, and equipmentState converts a Status
// to it.

package wiim

import (
	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// Status is the shape that the operator writes under status.wiim. A
// block that the device did not reply to stays empty.
//
// Status leaves out values that change between reads with no action
// from anyone: the amp's clock, its temperatures, the Wi-Fi signal,
// noise, and rate, the remote's battery and signal, the track position,
// and the seconds left on the sleep timer. Each of them would make
// every poll write the status, and each write wakes every watcher of
// every Receiver. Readings holds the measurements for the metrics, and
// the zone's Sleep has the timer in whole minutes.
type Status struct {
	Device     DeviceInfo  `json:"device"`
	Network    Network     `json:"network"`
	Playback   Playback    `json:"playback"`
	NowPlaying *NowPlaying `json:"nowPlaying,omitempty"`
	Audio      Audio       `json:"audio"`
	Equalizer  Equalizer   `json:"equalizer"`
	Timers     Timers      `json:"timers"`
	Bluetooth  Bluetooth   `json:"bluetooth"`
	Presets    []Preset    `json:"presets,omitempty"`
	Controls   Controls    `json:"controls"`
}

// DeviceInfo is the amp's identity and hardware, from getStatusEx. The
// operator identifies the amp by UUID.
type DeviceInfo struct {
	Name              string       `json:"name,omitempty"`
	Group             string       `json:"group,omitempty"`
	Model             string       `json:"model,omitempty"`
	Firmware          string       `json:"firmware,omitempty"`
	Build             string       `json:"build,omitempty"`
	Released          string       `json:"released,omitempty"`
	Hardware          string       `json:"hardware,omitempty"`
	BoardVersion      string       `json:"boardVersion,omitempty"`
	Uboot             string       `json:"uboot,omitempty"`
	MCU               string       `json:"mcu,omitempty"`
	HDMI              string       `json:"hdmi,omitempty"`
	UUID              string       `json:"uuid,omitempty"`
	TempUUID          string       `json:"tempUuid,omitempty"`
	UPnPUUID          string       `json:"upnpUuid,omitempty"`
	MACs              MACs         `json:"macs"`
	Internet          bool         `json:"internet"`
	Initialized       bool         `json:"initialized"`
	PrivacyMode       bool         `json:"privacyMode"`
	Language          string       `json:"language,omitempty"`
	Timezone          string       `json:"timezone,omitempty"`
	TimezoneOffset    string       `json:"timezoneOffset,omitempty"`
	CommunicationPort string       `json:"communicationPort,omitempty"`
	Capabilities      Capabilities `json:"capabilities"`
	Security          Security     `json:"security"`
}

// MACs are the device's hardware addresses for each interface.
type MACs struct {
	WiFi        string `json:"wifi,omitempty"`
	Bluetooth   string `json:"bluetooth,omitempty"`
	AccessPoint string `json:"accessPoint,omitempty"`
	Ethernet    string `json:"ethernet,omitempty"`
}

// Capabilities are the features that the device reports, as flags and
// numbers. The operator records them and does not act on them.
type Capabilities struct {
	Cast          bool   `json:"cast"`
	PresetKeys    int    `json:"presetKeys,omitempty"`
	MQTT          bool   `json:"mqtt"`
	AudioCast     bool   `json:"audioCast"`
	EQSupport     string `json:"eqSupport,omitempty"`
	EQVersion     string `json:"eqVersion,omitempty"`
	MaxVolume     int    `json:"maxVolume,omitempty"`
	VolumeControl int    `json:"volumeControl,omitempty"`
}

// Security is the security block of getStatusEx. It lists protocols
// that the device supports, not ones it requires: the HTTPS API
// replies to requests that carry no token.
type Security struct {
	Protocol string `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`
	AES      string `json:"aes,omitempty"`
}

// Network is the device's current address and link, and its static
// configuration. The link's signal, noise, and rate are in Readings.
type Network struct {
	Interface string    `json:"interface,omitempty"`
	Address   string    `json:"address,omitempty"`
	SSID      string    `json:"ssid,omitempty"`
	BSSID     string    `json:"bssid,omitempty"`
	Channel   int       `json:"channel,omitempty"`
	Frequency int       `json:"frequency,omitempty"`
	Static    *StaticIP `json:"static,omitempty"`
}

// StaticIP is the static configuration that the device stores for each
// interface. An interface that uses DHCP reports empty values.
type StaticIP struct {
	WiFi     StaticAddress `json:"wifi"`
	Ethernet StaticAddress `json:"ethernet"`
}

type StaticAddress struct {
	Address string `json:"address,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	DNS     string `json:"dns,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Playback is the reply to getPlayerStatus. The device sends the
// title, artist, and album hex-encoded, and the client decodes them.
type Playback struct {
	Status        string `json:"status,omitempty"`
	Mode          int    `json:"mode"`
	Source        string `json:"source,omitempty"`
	Loop          int    `json:"loop"`
	EQPreset      int    `json:"eqPreset"`
	Vendor        string `json:"vendor,omitempty"`
	Channel       int    `json:"channel"`
	Type          int    `json:"type"`
	Volume        int    `json:"volume"`
	Mute          bool   `json:"mute"`
	LengthMS      int    `json:"lengthMs"`
	AlarmFlag     bool   `json:"alarmFlag"`
	PlaylistCount int    `json:"playlistCount"`
	PlaylistIndex int    `json:"playlistIndex"`
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
}

// NowPlaying is the current track, from getMetaInfo. It is nil when
// the read fails or the device replies "Failed", which it does for a
// source with no track, so the status never shows an earlier track.
type NowPlaying struct {
	Title      string `json:"title,omitempty"`
	Subtitle   string `json:"subtitle,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	ArtURI     string `json:"artUri,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	BitDepth   int    `json:"bitDepth,omitempty"`
	BitRate    int    `json:"bitRate,omitempty"`
	TrackID    string `json:"trackId,omitempty"`
}

type Audio struct {
	Output                Output        `json:"output"`
	Balance               float64       `json:"balance"`
	MaxVolume             int           `json:"maxVolume,omitempty"`
	RemoteVolumeStep      int           `json:"remoteVolumeStep,omitempty"`
	ButtonVolumeStep      int           `json:"buttonVolumeStep,omitempty"`
	PlayModeVolumeEnabled bool          `json:"playModeVolumeEnabled"`
	PlayModeVolumes       []InputVolume `json:"playModeVolumes,omitempty"`
	Subwoofer             *Subwoofer    `json:"subwoofer,omitempty"`
	SPDIFDelayMS          int           `json:"spdifDelayMs,omitempty"`
}

type Output struct {
	Mode      int          `json:"mode"`
	Hardware  string       `json:"hardware,omitempty"`
	Name      string       `json:"name,omitempty"`
	CardID    string       `json:"cardId,omitempty"`
	Source    bool         `json:"source"`
	AudioCast bool         `json:"audioCast"`
	Available []OutputMode `json:"available,omitempty"`
}

// OutputMode is one output that the device offers, with the formats
// that its card accepts.
type OutputMode struct {
	Index       int    `json:"index"`
	Mode        string `json:"mode,omitempty"`
	Device      string `json:"device,omitempty"`
	CardID      string `json:"cardId,omitempty"`
	BitDepths   []int  `json:"bitDepths,omitempty"`
	SampleRates []int  `json:"sampleRates,omitempty"`
}

// InputVolume is the volume that the device stores for one input.
type InputVolume struct {
	Mode   int    `json:"mode"`
	Name   string `json:"name,omitempty"`
	Volume int    `json:"volume"`
	Stored bool   `json:"stored"`
	Active bool   `json:"active"`
}

// Subwoofer is the device's subwoofer settings. It is nil until the
// device replies to the subwoofer read, and Plugged tells whether a
// subwoofer is connected.
type Subwoofer struct {
	Plugged    bool    `json:"plugged"`
	OutputMode int     `json:"outputMode"`
	Crossover  float64 `json:"crossover"`
	Phase      float64 `json:"phase"`
	Level      float64 `json:"level"`
	Delay      float64 `json:"delay"`
	MainFilter bool    `json:"mainFilter"`
	SubFilter  bool    `json:"subFilter"`
	MixSub     bool    `json:"mixSub"`
}

type Equalizer struct {
	Enabled bool     `json:"enabled"`
	Preset  int      `json:"preset,omitempty"`
	Presets []string `json:"presets,omitempty"`
}

// Timers are the sleep timer, the alarms, and the idle intervals.
// SleepSeconds is -1 until the device reports the timer.
type Timers struct {
	SleepSeconds int       `json:"-"`
	Alarms       []Alarm   `json:"alarms,omitempty"`
	PowerMode    PowerMode `json:"powerMode"`
}

// Alarm is one alarm. The device has at most three, read with
// getAlarmClock:0 to getAlarmClock:2.
type Alarm struct {
	Index     int    `json:"index"`
	Enabled   bool   `json:"enabled"`
	Trigger   int    `json:"trigger,omitempty"`
	Operation int    `json:"operation,omitempty"`
	Time      string `json:"time,omitempty"`
	Day       string `json:"day,omitempty"`
	Path      string `json:"path,omitempty"`
}

// PowerMode is how long the device stays in each state before it moves
// to the next, in minutes.
type PowerMode struct {
	Idle       int `json:"idle,omitempty"`
	Standby    int `json:"standby,omitempty"`
	Sleep      int `json:"sleep,omitempty"`
	PowerOff   int `json:"powerOff,omitempty"`
	SemiActive int `json:"semiActive,omitempty"`
}

type Bluetooth struct {
	PairStatus int            `json:"pairStatus"`
	Paired     []PairedDevice `json:"paired,omitempty"`
	Remote     *BLERemote     `json:"remote,omitempty"`
}

type PairedDevice struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Role    string `json:"role,omitempty"`
}

// BLERemote is the Bluetooth remote that comes with the amp. Its
// battery and signal are in Readings.
type BLERemote struct {
	Supported bool   `json:"supported"`
	Connected bool   `json:"connected"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state,omitempty"`
}

type Preset struct {
	Number int    `json:"number"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
}

type Controls struct {
	LED      bool `json:"led"`
	Buttons  bool `json:"buttons"`
	CECPower bool `json:"cecPower"`
}

// newStatus is the state before the device has replied. A number that
// the device has not reported is equipment.Unknown, so that it does not
// read as a real zero.
func newStatus() Status {
	return Status{
		Playback: Playback{Volume: equipment.Unknown},
		Audio:    Audio{MaxVolume: equipment.Unknown},
		Timers:   Timers{SleepSeconds: -1},
	}
}

// zone reports Power as always on, because the API has no standby
// command and the device reports no other power state.
func (s Status) zone() equipment.ZoneState {
	return equipment.ZoneState{
		Power:           equipment.PowerOn,
		Input:           s.Playback.Source,
		Mute:            s.Playback.Mute,
		Volume:          s.Playback.Volume,
		VolumeMax:       s.Audio.MaxVolume,
		VolumeMaxStable: true,
		Sleep:           sleepMinutes(s.Timers.SleepSeconds),
	}
}

// sleepMinutes rounds a partial minute up, and returns
// equipment.Unknown when the device has not reported the timer.
func sleepMinutes(seconds int) int {
	if seconds < 0 {
		return equipment.Unknown
	}
	if seconds == 0 {
		return 0
	}
	return (seconds + 59) / 60
}

func (s Status) equipmentState(reachable equipment.ConditionStatus) equipment.State {
	return equipment.State{
		Reachable: reachable,
		Zones:     map[string]equipment.ZoneState{equipment.MainZone: s.zone()},
	}
}
