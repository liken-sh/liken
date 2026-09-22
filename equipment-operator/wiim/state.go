// The WiiM's own state: the strongly typed snapshot the status carries
// and the translation into the equipment contract. The snapshot is in
// the device's vocabulary and covers every observable the amps report;
// the contract is the normalized power, input, mute, and volume every
// Receiver shares.

package wiim

import (
	"github.com/liken-sh/equipment-operator/equipment"
)

// Status is everything one WiiM reports. It is the device's own
// vocabulary and the shape written under status.wiim. A block the
// device did not answer stays empty.
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

// DeviceInfo is the identity and the hardware: what the device is,
// which firmware it runs, and how it is addressed. The UUID is the
// identity the operator trusts. It is not the discovery Device, which
// the network search returns; this is the snapshot a poll reads.
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
	Date              string       `json:"date,omitempty"`
	Time              string       `json:"time,omitempty"`
	CommunicationPort string       `json:"communicationPort,omitempty"`
	TemperatureCPU    int          `json:"temperatureCpu,omitempty"`
	TemperatureBoard  int          `json:"temperatureBoard,omitempty"`
	Capabilities      Capabilities `json:"capabilities"`
	Security          Security     `json:"security"`
}

// MACs are the device's four hardware addresses. One device carries one
// each for Wi-Fi, Bluetooth, its access point, and Ethernet.
type MACs struct {
	WiFi        string `json:"wifi,omitempty"`
	Bluetooth   string `json:"bluetooth,omitempty"`
	AccessPoint string `json:"accessPoint,omitempty"`
	Ethernet    string `json:"ethernet,omitempty"`
}

// Capabilities is what the device reports it can do, as flags and
// numbers rather than a promise: the operator reads them and acts on
// none.
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

// Security is the block getStatusEx reports: what the device can do,
// not what it requires. The plain HTTPS API answers without a token.
type Security struct {
	Protocol string `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`
	AES      string `json:"aes,omitempty"`
}

// Network is the device's current address and link, plus the static
// configuration it holds.
type Network struct {
	Interface string    `json:"interface,omitempty"`
	Address   string    `json:"address,omitempty"`
	SSID      string    `json:"ssid,omitempty"`
	BSSID     string    `json:"bssid,omitempty"`
	RSSI      int       `json:"rssi,omitempty"`
	Noise     int       `json:"noise,omitempty"`
	SNR       int       `json:"snr,omitempty"`
	Channel   int       `json:"channel,omitempty"`
	Frequency int       `json:"frequency,omitempty"`
	DataRate  int       `json:"dataRate,omitempty"`
	Static    *StaticIP `json:"static,omitempty"`
}

// StaticIP is the static configuration the device holds for each
// interface. An interface on DHCP reports empty values.
type StaticIP struct {
	WiFi     StaticAddress `json:"wifi"`
	Ethernet StaticAddress `json:"ethernet"`
}

// StaticAddress is one interface's static configuration.
type StaticAddress struct {
	Address string `json:"address,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	DNS     string `json:"dns,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Playback is the player's own report: what it is doing, what source it
// is on, and the transport fields. The title fields carry the player's
// hex-encoded text.
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
	PositionMS    int    `json:"positionMs"`
	OffsetMS      int    `json:"offsetMs"`
	LengthMS      int    `json:"lengthMs"`
	AlarmFlag     bool   `json:"alarmFlag"`
	PlaylistCount int    `json:"playlistCount"`
	PlaylistIndex int    `json:"playlistIndex"`
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
}

// NowPlaying is the current track as plain JSON. It is absent when the
// source has no track: the device answers "Failed" to getMetaInfo then,
// and a stale track would be worse than none.
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

// Audio is the output block and the level controls around it.
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

// Output is the hardware output the device drives.
type Output struct {
	Mode      int          `json:"mode"`
	Hardware  string       `json:"hardware,omitempty"`
	Name      string       `json:"name,omitempty"`
	CardID    string       `json:"cardId,omitempty"`
	Source    bool         `json:"source"`
	AudioCast bool         `json:"audioCast"`
	Available []OutputMode `json:"available,omitempty"`
}

// OutputMode is one output the device offers, with the formats it
// accepts on that card.
type OutputMode struct {
	Index       int    `json:"index"`
	Mode        string `json:"mode,omitempty"`
	Device      string `json:"device,omitempty"`
	CardID      string `json:"cardId,omitempty"`
	BitDepths   []int  `json:"bitDepths,omitempty"`
	SampleRates []int  `json:"sampleRates,omitempty"`
}

// InputVolume is the level the device stores for one input, and whether
// that input is the active one.
type InputVolume struct {
	Mode   int    `json:"mode"`
	Name   string `json:"name,omitempty"`
	Volume int    `json:"volume"`
	Stored bool   `json:"stored"`
	Active bool   `json:"active"`
}

// Subwoofer is the crossover and level block the device reports when a
// subwoofer is configured.
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

// Equalizer is the equalizer's state and its available presets.
type Equalizer struct {
	Enabled bool     `json:"enabled"`
	Preset  int      `json:"preset,omitempty"`
	Presets []string `json:"presets,omitempty"`
}

// Timers is the sleep timer, the alarm slots, and the idle intervals
// the device holds.
type Timers struct {
	SleepSeconds int       `json:"sleepSeconds"`
	Alarms       []Alarm   `json:"alarms,omitempty"`
	PowerMode    PowerMode `json:"powerMode"`
}

// Alarm is one of the device's three alarm slots.
type Alarm struct {
	Index     int    `json:"index"`
	Enabled   bool   `json:"enabled"`
	Trigger   int    `json:"trigger,omitempty"`
	Operation int    `json:"operation,omitempty"`
	Time      string `json:"time,omitempty"`
	Day       string `json:"day,omitempty"`
	Path      string `json:"path,omitempty"`
}

// PowerMode is how long the device waits in each state before it moves
// on, in minutes.
type PowerMode struct {
	Idle       int `json:"idle,omitempty"`
	Standby    int `json:"standby,omitempty"`
	Sleep      int `json:"sleep,omitempty"`
	PowerOff   int `json:"powerOff,omitempty"`
	SemiActive int `json:"semiActive,omitempty"`
}

// Bluetooth is the pairing state, the paired devices, and the remote.
type Bluetooth struct {
	PairStatus int            `json:"pairStatus"`
	Paired     []PairedDevice `json:"paired,omitempty"`
	Remote     *BLERemote     `json:"remote,omitempty"`
}

// PairedDevice is one device the amp has paired with.
type PairedDevice struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Role    string `json:"role,omitempty"`
}

// BLERemote is the Bluetooth remote the amp ships with.
type BLERemote struct {
	Supported bool   `json:"supported"`
	Connected bool   `json:"connected"`
	Battery   int    `json:"battery,omitempty"`
	RSSI      int    `json:"rssi,omitempty"`
	Version   string `json:"version,omitempty"`
	State     string `json:"state,omitempty"`
}

// Preset is one stored preset the device can start.
type Preset struct {
	Number int    `json:"number"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
}

// Controls is the device's own controls: the status light, the touch
// buttons, and CEC.
type Controls struct {
	LED      bool `json:"led"`
	Buttons  bool `json:"buttons"`
	CECPower bool `json:"cecPower"`
}

// newStatus is the state before the device has answered. A number the
// device has not reported is unknown, so a zero the device never said
// does not read as a real answer.
func newStatus() Status {
	return Status{
		Playback: Playback{Volume: equipment.Unknown},
		Audio:    Audio{MaxVolume: equipment.Unknown},
		Timers:   Timers{SleepSeconds: -1},
	}
}

// zone is the contract's main zone built from the snapshot. Power is
// always on: the API has no standby command, so the device has no
// state to report but on.
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

// sleepMinutes writes the sleep timer in whole minutes, rounding up a
// partial minute, and unknown when the device has not reported one.
func sleepMinutes(seconds int) int {
	if seconds < 0 {
		return equipment.Unknown
	}
	if seconds == 0 {
		return 0
	}
	return (seconds + 59) / 60
}

// equipmentState maps the snapshot onto the contract: one main zone and
// the reachability verdict the client holds.
func (s Status) equipmentState(reachable equipment.ConditionStatus) equipment.State {
	return equipment.State{
		Reachable: reachable,
		Zones:     map[string]equipment.ZoneState{equipment.MainZone: s.zone()},
	}
}
