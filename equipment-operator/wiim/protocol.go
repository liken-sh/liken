// The WiiM control protocol. wiim/AGENTS.md lists the source of each
// command and records which commands a live Amp answers.

package wiim

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// Port is the port of the control API. The API needs no authentication
// and uses HTTPS with a self-signed certificate, so the client does not
// verify the certificate.
const Port = "443"

const apiPath = "/httpapi.asp"

const (
	statusCommand       = "getStatusEx"
	playerCommand       = "getPlayerStatus"
	metaCommand         = "getMetaInfo"
	balanceCommand      = "getChannelBalance"
	eqStatCommand       = "EQGetStat"
	eqListCommand       = "EQGetList"
	shutdownCommand     = "getShutdown"
	outputModeCommand   = "getNewAudioOutputHardwareMode"
	outputListCommand   = "getSoundCardModeSupportList"
	activeOutputCommand = "getActiveSoundCardOutputMode"
	subCommand          = "getSubLPF"
	spdifCommand        = "getSpdifOutSwitchDelayMs"
	playModeEnable      = "getPlayModeVolumeEnable"
	playModeCommand     = "getPlayModeVolumeValue"
	remoteStepCommand   = "get_remote_volume_step"
	buttonStepCommand   = "get_button_volume_step"
	cecCommand          = "getCecPowerCtrl"
	powerModeCommand    = "getPowerModeTime"
	staticIPCommand     = "getStaticIpInfo"
	presetCommand       = "getPresetInfo"
	alarmCommand        = "getAlarmClock"
	btPairCommand       = "getbtpairstatus"
	btHistoryCommand    = "getbthistory"
	ledCommand          = "LED_SWITCH_GET"
	buttonsCommand      = "Button_Enable_GET"
)

// A command that changes state is the prefix followed by the argument.
const (
	setVolumePrefix   = "setPlayerCmd:vol:"
	setMutePrefix     = "setPlayerCmd:mute:"
	setInputPrefix    = "setPlayerCmd:switchmode:"
	setShutdownPrefix = "setShutdown:"
)

// The outcomes of a command, as values of the label on the command
// counter in the root package. They equal the Denon driver's values,
// so one metric counts the commands of both drivers.
const (
	CommandOK      = "ok"
	CommandFailed  = "failed"
	CommandTimeout = "timeout"
)

// commandURL puts command into the query string without escaping,
// with its arguments separated by colons, as the device's documents
// spell it.
func commandURL(address, command string) string {
	return "https://" + hostPort(address) + apiPath + "?command=" + command
}

func hostPort(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, Port)
}

// inputForMode maps the mode number of getPlayerStatus to an input
// name. Every mode not listed here maps to "wifi", because the other
// modes are streaming services that the amp plays over the network.
// cvdlinden/wiim-httpapi's openapi.json lists the numbers.
func inputForMode(mode int) string {
	switch mode {
	case 0:
		return ""
	case 40, 47:
		return "line-in"
	case 41:
		return "bluetooth"
	case 43:
		return "optical"
	case 49:
		return "hdmi"
	case 51:
		return "usb"
	case 11, 16:
		return "udisk"
	}
	return "wifi"
}

// modeForInput returns the switchmode argument for input, and false
// when the driver does not know the input.
func modeForInput(input string) (string, bool) {
	switch input {
	case "line-in":
		return "line-in", true
	case "bluetooth":
		return "bluetooth", true
	case "optical":
		return "optical", true
	case "hdmi":
		return "hdmi", true
	case "usb":
		return "usb", true
	case "udisk":
		return "udisk", true
	case "wifi":
		return "wifi", true
	}
	return "", false
}

// NormalizeUUID returns the first twelve bytes of a LinkPlay UUID as
// upper-case hex, so every spelling of one device's identity compares
// equal. getStatusEx returns the twelve bytes alone, mDNS and SSDP
// return sixteen bytes that start with the same twelve, and a person
// can write either with dashes or colons.
func NormalizeUUID(value string) string { return normalizeUUID(value) }

func normalizeUUID(value string) string {
	cleaned := strings.ToUpper(value)
	cleaned = strings.NewReplacer("-", "", ":", "", "UUID:", "").Replace(cleaned)
	if len(cleaned) < 24 {
		return cleaned
	}
	return cleaned[:24]
}

// decodeHex decodes the hex that getPlayerStatus sends for the title,
// artist, and album. A value that is not valid hex decodes to "".
func decodeHex(value string) string {
	if value == "" {
		return ""
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func atoi(value string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return number, true
}

func atof(value string) (float64, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return number, true
}

// flexString decodes from a JSON string or a JSON number. Some fields
// are a string on one firmware and a number on another, and a field of
// a fixed type would fail the decode of the whole response.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	*f = flexString(strings.Trim(strings.TrimSpace(string(data)), `"`))
	return nil
}

// statusEx is the response of getStatusEx, the device information.
type statusEx struct {
	Language             string     `json:"language"`
	DeviceName           string     `json:"DeviceName"`
	GroupName            string     `json:"GroupName"`
	SSID                 string     `json:"ssid"`
	Group                string     `json:"group"`
	Firmware             string     `json:"firmware"`
	Build                string     `json:"build"`
	Project              string     `json:"project"`
	Release              string     `json:"Release"`
	Hardware             string     `json:"hardware"`
	PCBVersion           string     `json:"PCB_version"`
	MCUVer               string     `json:"mcu_ver"`
	HDMIVer              string     `json:"hdmi_ver"`
	UbootVer             string     `json:"uboot_verinfo"`
	UUID                 string     `json:"uuid"`
	MAC                  string     `json:"MAC"`
	BTMAC                string     `json:"BTMAC"`
	APMAC                string     `json:"AP_MAC"`
	ETHMAC               string     `json:"ETH_MAC"`
	TempUUID             string     `json:"temp_uuid"`
	UPnPUUID             string     `json:"upnp_uuid"`
	Internet             string     `json:"internet"`
	Timezone             string     `json:"tz"`
	TimezoneID           string     `json:"app_timezone_id"`
	PrivacyMode          string     `json:"privacy_mode"`
	InitialConfiguration string     `json:"InitialConfiguration"`
	CommunicationPort    string     `json:"communication_port"`
	CastEnable           flexString `json:"cast_enable"`
	PresetKey            string     `json:"preset_key"`
	MQTTSupport          string     `json:"mqtt_support"`
	AudioCast            string     `json:"audiocast"`
	MaxVolume            string     `json:"max_volume"`
	VolumeControl        string     `json:"volume_control"`
	EQSupport            string     `json:"EQ_support"`
	EQVersion            string     `json:"EQVersion"`
	Security             string     `json:"security"`
	SecurityVersion      string     `json:"security_version"`
	SecurityCapabilities struct {
		Ver    string `json:"ver"`
		AESVer string `json:"aes_ver"`
	} `json:"security_capabilities"`
	Apcli0             string `json:"apcli0"`
	Eth0               string `json:"eth0"`
	Essid              string `json:"essid"`
	WifiChannel        string `json:"WifiChannel"`
	RSSI               string `json:"RSSI"`
	BSSID              string `json:"BSSID"`
	WlanSnr            string `json:"wlanSnr"`
	WlanNoise          string `json:"wlanNoise"`
	WlanFreq           string `json:"wlanFreq"`
	WlanDataRate       string `json:"wlanDataRate"`
	TemperatureCPU     string `json:"temperature_cpu"`
	TemperatureTmp102  string `json:"temperature_tmp102"`
	BleRemoteControl   string `json:"BleRemoteControl"`
	BleRemoteConnected string `json:"BleRemoteConnected"`
	BleRemoteBattery   string `json:"BleRemoteBatterylevel"`
	BleRemoteRSSI      string `json:"BleRemoteRSSI"`
	BleRemoteVersion   string `json:"BleRemoteVersion"`
	BleRemoteState     string `json:"BleRemote_dev_state"`
}

// playerStatus is the response of getPlayerStatus and
// getPlayerStatusEx, the playback state.
type playerStatus struct {
	Type          string `json:"type"`
	Channel       string `json:"ch"`
	Mode          string `json:"mode"`
	Loop          string `json:"loop"`
	EQ            string `json:"eq"`
	Vendor        string `json:"vendor"`
	Status        string `json:"status"`
	TotalLength   string `json:"totlen"`
	Title         string `json:"Title"`
	Artist        string `json:"Artist"`
	Album         string `json:"Album"`
	AlarmFlag     string `json:"alarmflag"`
	PlaylistCount string `json:"plicount"`
	PlaylistIndex string `json:"plicurr"`
	Volume        string `json:"vol"`
	Mute          string `json:"mute"`
}

// metaInfo is the response of getMetaInfo, the track now playing.
type metaInfo struct {
	MetaData struct {
		Album       string `json:"album"`
		Title       string `json:"title"`
		Subtitle    string `json:"subtitle"`
		Artist      string `json:"artist"`
		AlbumArtURI string `json:"albumArtURI"`
		SampleRate  string `json:"sampleRate"`
		BitDepth    string `json:"bitDepth"`
		BitRate     string `json:"bitRate"`
		TrackID     string `json:"trackId"`
	} `json:"metaData"`
}

// outputMode is the response of getNewAudioOutputHardwareMode. The Amp
// returns hardware values that the published enum does not list, so
// Hardware is the string as the device sent it.
type outputMode struct {
	Hardware  string `json:"hardware"`
	Source    string `json:"source"`
	AudioCast string `json:"audiocast"`
}

// outputModeListEntry is one entry of the response of
// getSoundCardModeSupportList.
type outputModeListEntry struct {
	Index     int    `json:"index"`
	Mode      string `json:"mode"`
	SoundCard struct {
		CardName    string `json:"cardName"`
		DeviceName  string `json:"devName"`
		CardID      string `json:"cardId"`
		BitDepths   []int  `json:"bitDepthSupportList"`
		SampleRates []int  `json:"sampleRateSupportList"`
	} `json:"soundCard"`
}

// activeOutput is the response of getActiveSoundCardOutputMode, the
// output the device is using.
type activeOutput struct {
	Index      string `json:"index"`
	Mode       string `json:"mode"`
	CardID     string `json:"cardId"`
	DeviceName string `json:"devName"`
	BTSource   string `json:"btSource"`
	AudioCast  string `json:"audioCast"`
}

// subwoofer is the response of getSubLPF, the subwoofer settings.
type subwoofer struct {
	DelayMainSub string  `json:"delay_main_sub"`
	Plugged      int     `json:"plugged"`
	OutputMode   int     `json:"output_mode"`
	Cross        float64 `json:"cross"`
	Phase        float64 `json:"phase"`
	Level        float64 `json:"level"`
	MixSub       int     `json:"mix_sub"`
	MainFilter   int     `json:"main_filter"`
	SubFilter    int     `json:"sub_filter"`
	SubDelay     float64 `json:"sub_delay"`
}

// perInputVolume is one entry of the response of
// getPlayModeVolumeValue, the volume the device stores for one input.
type perInputVolume struct {
	Mode   int    `json:"mode"`
	Name   string `json:"name"`
	Volume int    `json:"volume"`
	Stored int    `json:"stored"`
	Active int    `json:"active"`
}

// powerModeTime is the response of getPowerModeTime, the intervals
// before each power mode.
type powerModeTime struct {
	IdleInterval       string `json:"idleInterval"`
	StandbyInterval    string `json:"standbyInterval"`
	SleepInterval      string `json:"sleepInterval"`
	PowerOffInterval   string `json:"poweroffInterval"`
	SemiActiveInterval string `json:"semiactiveInterval"`
}

// staticIPInfo is the response of getStaticIpInfo.
type staticIPInfo struct {
	WlanStaticIP       string `json:"wlanStaticIp"`
	WlanGateWay        string `json:"wlanGateWay"`
	WlanDNSServer      string `json:"wlanDnsServer"`
	WlanStaticIPEnable int    `json:"wlanStaticIpEnable"`
	EthStaticIP        string `json:"ethStaticIp"`
	EthGateWay         string `json:"ethGateWay"`
	EthDNSServer       string `json:"ethDnsServer"`
	EthStaticIPEnable  int    `json:"ethStaticIpEnable"`
}

// presetInfo is the response of getPresetInfo, the preset list.
type presetInfo struct {
	PresetNum  int `json:"preset_num"`
	PresetList []struct {
		Number int    `json:"number"`
		Name   string `json:"name"`
		URL    string `json:"url"`
		Source string `json:"source"`
		PicURL string `json:"picurl"`
	} `json:"preset_list"`
}

// alarmClock is the response of getAlarmClock:n, one alarm.
type alarmClock struct {
	Enable    string `json:"enable"`
	Trigger   string `json:"trigger"`
	Operation string `json:"operation"`
	Date      string `json:"date"`
	WeekDay   string `json:"week_day"`
	Day       string `json:"day"`
	Time      string `json:"time"`
	Path      string `json:"path"`
}

// btHistory is the response of getbthistory, the Bluetooth device
// history.
type btHistory struct {
	Num        int `json:"num"`
	ScanStatus int `json:"scan_status"`
	List       []struct {
		Name    string `json:"name"`
		Address string `json:"ad"`
		Role    string `json:"role"`
		RSSI    int    `json:"rssi"`
	} `json:"list"`
}

// btPairStatus is the response of getbtpairstatus.
type btPairStatus struct {
	Result int `json:"result"`
}

// stepAnswer is the response of get_remote_volume_step and
// get_button_volume_step.
type stepAnswer struct {
	Status           string `json:"status"`
	RemoteVolumeStep int    `json:"remote_volume_step"`
	ButtonVolumeStep int    `json:"button_volume_step"`
}

// eqAnswer is the response of EQGetStat.
type eqAnswer struct {
	EQStat string `json:"EQStat"`
	Status string `json:"status"`
}

// IsWiiM reports whether name, a getStatusEx project or the modelName
// of the UPnP description, begins with "WiiM" in any letter case. Other
// brands build on LinkPlay's platform and answer the same commands, so
// only the name tells a WiiM apart. A WiiM's project is like
// WiiM_Amp_4layer and its model like WiiM Amp. Another brand's names
// are not, such as the project ARYLIC_A50TE and the model A50. The
// manufacturer does not help, because LinkPlay makes devices that other
// brands sell.
func IsWiiM(name string) bool {
	return len(name) >= 4 && strings.EqualFold(name[:4], "wiim")
}
