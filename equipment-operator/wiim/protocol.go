// The WiiM control protocol: the command names, the HTTPS GET that
// carries them, and the shape of each answer. wiim/AGENTS.md holds the
// sources for every fact here and records which commands a live Amp
// answers.

package wiim

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// Port is the control port. The API is unauthenticated HTTPS, and the
// certificate is self-signed, so a client skips verification.
const Port = "443"

// apiPath is the one path every command goes through.
const apiPath = "/httpapi.asp"

// The commands this driver reads. Every one is a GET.
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

// The setters, each a GET with its argument in the command name.
const (
	setVolumePrefix   = "setPlayerCmd:vol:"
	setMutePrefix     = "setPlayerCmd:mute:"
	setInputPrefix    = "setPlayerCmd:switchmode:"
	setShutdownPrefix = "setShutdown:"
)

// The outcomes a command can carry, the label values the root's
// command counter already counts. They match the Denon driver's, so one
// metric counts both protocols.
const (
	CommandOK      = "ok"
	CommandFailed  = "failed"
	CommandTimeout = "timeout"
)

// commandURL is the full URL for one command. The command carries its
// arguments with colons and goes on the query string the way the
// device's own documents spell it.
func commandURL(address, command string) string {
	return "https://" + hostPort(address) + apiPath + "?command=" + command
}

// hostPort fills in the control port when the declared address names
// none.
func hostPort(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, Port)
}

// inputForMode maps the player's mode number onto the input name the amp
// carries. A streaming mode is the network input, because the amp
// reached it over the network. The community OpenAPI documents the
// numbers.
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

// modeForInput is the reverse, for a SetInput. An input this driver does
// not know is an error at the caller.
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

// normalizeUUID holds the twelve bytes of the LinkPlay UUID as upper
// hex. getStatusEx spells it bare; mDNS and SSDP spell a sixteen-byte
// UUID whose first twelve bytes are the same value, so the first
// twenty-four hex characters are the identity.
func normalizeUUID(value string) string {
	cleaned := strings.ToUpper(value)
	cleaned = strings.NewReplacer("-", "", ":", "", "UUID:", "").Replace(cleaned)
	if len(cleaned) < 24 {
		return cleaned
	}
	return cleaned[:24]
}

// decodeHex turns the player's hex-encoded text into a string. The
// player encodes title, artist, and album as hex bytes; a malformed
// value reads as empty.
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

// atoi reads a number the device sent as a string, and reports whether
// it was one.
func atoi(value string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return number, true
}

// atof reads a decimal the device sent as a string or a bare number.
func atof(value string) (float64, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, false
	}
	return number, true
}

// flexString reads a value the device sends either as a JSON string or
// as a bare number, and holds it as text. The same field is a string on
// one firmware and a number on another, and a struct that insists on one
// shape fails the whole answer over a single field.
type flexString string

// UnmarshalJSON reads the value and strips the quotes a string carries.
func (f *flexString) UnmarshalJSON(data []byte) error {
	*f = flexString(strings.Trim(strings.TrimSpace(string(data)), `"`))
	return nil
}

// statusEx is the device block getStatusEx answers.
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
	Date                 string     `json:"date"`
	Time                 string     `json:"time"`
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

// playerStatus is the playback block getPlayerStatus and
// getPlayerStatusEx answer.
type playerStatus struct {
	Type            string `json:"type"`
	Channel         string `json:"ch"`
	Mode            string `json:"mode"`
	Loop            string `json:"loop"`
	EQ              string `json:"eq"`
	Vendor          string `json:"vendor"`
	Status          string `json:"status"`
	CurrentPosition string `json:"curpos"`
	Offset          string `json:"offset_pts"`
	TotalLength     string `json:"totlen"`
	Title           string `json:"Title"`
	Artist          string `json:"Artist"`
	Album           string `json:"Album"`
	AlarmFlag       string `json:"alarmflag"`
	PlaylistCount   string `json:"plicount"`
	PlaylistIndex   string `json:"plicurr"`
	Volume          string `json:"vol"`
	Mute            string `json:"mute"`
}

// metaInfo is the now-playing block getMetaInfo answers.
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

// outputMode is the hardware output getNewAudioOutputHardwareMode
// answers. The Amp returns a hardware value the published enum does not
// list, so the number is kept as it came.
type outputMode struct {
	Hardware  string `json:"hardware"`
	Source    string `json:"source"`
	AudioCast string `json:"audiocast"`
}

// outputModeListEntry is one entry getSoundCardModeSupportList answers.
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

// activeOutput is the block getActiveSoundCardOutputMode answers: the
// output the device is driving now.
type activeOutput struct {
	Index      string `json:"index"`
	Mode       string `json:"mode"`
	CardID     string `json:"cardId"`
	DeviceName string `json:"devName"`
	BTSource   string `json:"btSource"`
	AudioCast  string `json:"audioCast"`
}

// subwoofer is the sub block getSubLPF answers.
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

// perInputVolume is one entry getPlayModeVolumeValue answers: the level
// the device stores for one input.
type perInputVolume struct {
	Mode   int    `json:"mode"`
	Name   string `json:"name"`
	Volume int    `json:"volume"`
	Stored int    `json:"stored"`
	Active int    `json:"active"`
}

// powerModeTime is the interval block getPowerModeTime answers.
type powerModeTime struct {
	IdleInterval       string `json:"idleInterval"`
	StandbyInterval    string `json:"standbyInterval"`
	SleepInterval      string `json:"sleepInterval"`
	PowerOffInterval   string `json:"poweroffInterval"`
	SemiActiveInterval string `json:"semiactiveInterval"`
}

// staticIPInfo is the block getStaticIpInfo answers.
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

// presetInfo is the block getPresetInfo answers.
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

// alarmClock is one slot getAlarmClock:n answers.
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

// btHistory is the block getbthistory answers.
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

// btPairStatus is the block getbtpairstatus answers.
type btPairStatus struct {
	Result int `json:"result"`
}

// stepAnswer is the block the volume-step reads answer.
type stepAnswer struct {
	Status           string `json:"status"`
	RemoteVolumeStep int    `json:"remote_volume_step"`
	ButtonVolumeStep int    `json:"button_volume_step"`
}

// eqAnswer is the block EQGetStat answers.
type eqAnswer struct {
	EQStat string `json:"EQStat"`
	Status string `json:"status"`
}
