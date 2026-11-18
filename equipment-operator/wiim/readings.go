// An amp's temperatures, Wi-Fi link, and remote change on almost every
// poll, so the operator publishes them as metrics and leaves them out of
// the status, which it would otherwise write on every poll.

package wiim

// Readings are the latest measurements from the amp's getStatusEx
// response. A field is nil when the amp did not report a number for it,
// such as the Wi-Fi fields while the amp is on its wired port, or the
// remote's fields while no remote is connected.
type Readings struct {
	// TemperatureCPU and TemperatureBoard are the SoC's and the board
	// sensor's temperatures, in degrees Celsius.
	TemperatureCPU   *int
	TemperatureBoard *int
	// WiFiSignal and WiFiNoise are in dBm, WiFiSNR in dB, and WiFiRate is
	// the link's rate in Mbit/s.
	WiFiSignal *int
	WiFiNoise  *int
	WiFiSNR    *int
	WiFiRate   *int
	// RemoteBattery is the remote's charge in percent, and RemoteSignal
	// its signal at the amp in dBm.
	RemoteBattery *int
	RemoteSignal  *int
}

// readingsFrom extracts the measurements from one getStatusEx
// response. The amp reports zeros for the Wi-Fi fields while it is on
// its wired port, so readingsFrom reads them only when onWiFi is true.
func readingsFrom(d statusEx, onWiFi bool) Readings {
	readings := Readings{
		TemperatureCPU:   reading(d.TemperatureCPU),
		TemperatureBoard: reading(d.TemperatureTmp102),
	}
	if onWiFi {
		readings.WiFiSignal = reading(d.RSSI)
		readings.WiFiNoise = reading(d.WlanNoise)
		readings.WiFiSNR = reading(d.WlanSnr)
		readings.WiFiRate = reading(d.WlanDataRate)
	}
	if d.BleRemoteConnected == "1" {
		readings.RemoteBattery = reading(d.BleRemoteBattery)
		readings.RemoteSignal = reading(d.BleRemoteRSSI)
	}
	return readings
}

func reading(value string) *int {
	number, ok := atoi(value)
	if !ok {
		return nil
	}
	return &number
}

// Readings returns the latest measurements. It is safe to call while
// Run is running.
func (c *Client) Readings() Readings {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.readings
}
