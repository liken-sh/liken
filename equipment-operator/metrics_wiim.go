package main

// The WiiM readings: the temperatures, the Wi-Fi link, and the remote
// that an amp reports in getStatusEx. Each one changes between two
// polls with nothing moved at the amp, so a Receiver's status leaves
// them out, because a status that changed on every poll would write the
// Receiver every ten seconds to every watcher. A gauge holds the same
// fact for a dashboard or an alert at no cost to the API server.

import (
	"github.com/liken-sh/equipment-operator/wiim"
	"github.com/prometheus/client_golang/prometheus"
)

// wiimGauges are the gauges of the readings, each labeled by the
// Receiver.
type wiimGauges struct {
	temperature   *prometheus.GaugeVec
	wifiSignal    *prometheus.GaugeVec
	wifiNoise     *prometheus.GaugeVec
	wifiSNR       *prometheus.GaugeVec
	wifiBitrate   *prometheus.GaugeVec
	remoteBattery *prometheus.GaugeVec
	remoteSignal  *prometheus.GaugeVec
}

func newWiimGauges() wiimGauges {
	gauge := func(name, help string, labels ...string) *prometheus.GaugeVec {
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: metricsPrefix + "wiim_" + name, Help: help}, append([]string{"receiver"}, labels...))
	}
	return wiimGauges{
		temperature:   gauge("temperature_celsius", "The temperature a WiiM amp last reported, by sensor: cpu or board.", "sensor"),
		wifiSignal:    gauge("wifi_signal_dbm", "The Wi-Fi signal strength a WiiM amp on Wi-Fi last reported."),
		wifiNoise:     gauge("wifi_noise_dbm", "The Wi-Fi noise level a WiiM amp on Wi-Fi last reported."),
		wifiSNR:       gauge("wifi_snr_db", "The Wi-Fi signal-to-noise ratio a WiiM amp on Wi-Fi last reported."),
		wifiBitrate:   gauge("wifi_bitrate_bits_per_second", "The Wi-Fi link rate a WiiM amp on Wi-Fi last reported."),
		remoteBattery: gauge("remote_battery_ratio", "The charge of a WiiM amp's connected remote, from 0 to 1."),
		remoteSignal:  gauge("remote_signal_dbm", "The signal strength of a WiiM amp's connected remote at the amp."),
	}
}

func (g wiimGauges) all() []*prometheus.GaugeVec {
	return []*prometheus.GaugeVec{g.temperature, g.wifiSignal, g.wifiNoise, g.wifiSNR, g.wifiBitrate, g.remoteBattery, g.remoteSignal}
}

func (g wiimGauges) collectors() []prometheus.Collector {
	var collectors []prometheus.Collector
	for _, gauge := range g.all() {
		collectors = append(collectors, gauge)
	}
	return collectors
}

// recordWiim puts one amp's readings on the gauges. A reading the amp
// did not report deletes its series, so a radio reading of an amp that
// moved to its wired port does not stand at its last value.
func (m *metrics) recordWiim(receiver string, readings wiim.Readings) {
	if m == nil {
		return
	}
	g := m.wiim
	setReading(g.temperature, readings.TemperatureCPU, 1, receiver, "cpu")
	setReading(g.temperature, readings.TemperatureBoard, 1, receiver, "board")
	setReading(g.wifiSignal, readings.WiFiSignal, 1, receiver)
	setReading(g.wifiNoise, readings.WiFiNoise, 1, receiver)
	setReading(g.wifiSNR, readings.WiFiSNR, 1, receiver)
	// The amp reports the rate in Mbit/s.
	setReading(g.wifiBitrate, readings.WiFiRate, 1e6, receiver)
	// The amp reports the charge in percent.
	setReading(g.remoteBattery, readings.RemoteBattery, 0.01, receiver)
	setReading(g.remoteSignal, readings.RemoteSignal, 1, receiver)
}

// setReading sets one series to value times scale, or deletes it when
// value is nil.
func setReading(gauge *prometheus.GaugeVec, value *int, scale float64, labels ...string) {
	if value == nil {
		gauge.DeleteLabelValues(labels...)
		return
	}
	gauge.WithLabelValues(labels...).Set(float64(*value) * scale)
}

// forget deletes every series of one Receiver.
func (g wiimGauges) forget(receiver string) {
	for _, gauge := range g.all() {
		gauge.DeletePartialMatch(prometheus.Labels{"receiver": receiver})
	}
}
