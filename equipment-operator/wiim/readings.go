// The measurements an amp reports that change on their own: its
// temperatures, its Wi-Fi link, and its remote. They change between two
// polls with nothing moved at the amp, so the status leaves them out and
// the operator publishes them as metrics.

package wiim

// Readings are the latest measurements getStatusEx carried. A nil field
// is one the amp did not report: a radio reading of an amp on its wired
// port, or a remote reading while no remote is connected.
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

// readingsFrom takes the measurements out of one getStatusEx answer.
// The amp reports zeros for the radio while it is on its wired port, so
// the radio readings are taken only while it is on Wi-Fi.
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

// reading is a number the amp sent as a string, or nil when it is not
// one.
func reading(value string) *int {
	number, ok := atoi(value)
	if !ok {
		return nil
	}
	return &number
}

// Readings returns the latest measurements, and can run concurrently
// with Run.
func (c *Client) Readings() Readings {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.readings
}
