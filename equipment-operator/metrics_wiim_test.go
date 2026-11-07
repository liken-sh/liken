package main

// The WiiM readings on the scrape: the values an amp reports that
// change on their own, and so stay out of the Receiver's status.

import (
	"testing"

	"github.com/liken-sh/equipment-operator/wiim"
)

func number(value int) *int { return &value }

// wifiReadings is what an amp on Wi-Fi with its remote connected
// reports.
func wifiReadings() wiim.Readings {
	return wiim.Readings{
		TemperatureCPU: number(43), TemperatureBoard: number(45),
		WiFiSignal: number(-46), WiFiNoise: number(-92), WiFiSNR: number(35), WiFiRate: number(480),
		RemoteBattery: number(60), RemoteSignal: number(-61),
	}
}

func TestWiimReadingsReachTheScrape(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)

	m.recordWiim("den", wifiReadings())

	body := scrape(t, m)
	requireSeries(t, body, `equipment_wiim_temperature_celsius{receiver="den",sensor="cpu"} 43`)
	requireSeries(t, body, `equipment_wiim_temperature_celsius{receiver="den",sensor="board"} 45`)
	requireSeries(t, body, `equipment_wiim_wifi_signal_dbm{receiver="den"} -46`)
	requireSeries(t, body, `equipment_wiim_wifi_noise_dbm{receiver="den"} -92`)
	requireSeries(t, body, `equipment_wiim_wifi_snr_db{receiver="den"} 35`)
	requireSeries(t, body, `equipment_wiim_wifi_bitrate_bits_per_second{receiver="den"} 4.8e+08`)
	requireSeries(t, body, `equipment_wiim_remote_battery_ratio{receiver="den"} 0.6`)
	requireSeries(t, body, `equipment_wiim_remote_signal_dbm{receiver="den"} -61`)
}

// A reading the amp stops reporting, such as the radio's once the amp
// moves to its wired port, leaves the scrape rather than stand at its
// last value.
func TestAWiimReadingTheAmpStopsReportingLeavesTheScrape(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	m.recordWiim("den", wifiReadings())

	wired := wifiReadings()
	wired.WiFiSignal, wired.WiFiNoise, wired.WiFiSNR, wired.WiFiRate = nil, nil, nil, nil
	m.recordWiim("den", wired)

	body := scrape(t, m)
	requireNoSeries(t, body, "equipment_wiim_wifi_signal_dbm")
	requireNoSeries(t, body, "equipment_wiim_wifi_bitrate_bits_per_second")
	requireSeries(t, body, `equipment_wiim_temperature_celsius{receiver="den",sensor="cpu"} 43`)
}

func TestForgetReceiverRemovesTheWiimReadings(t *testing.T) {
	t.Parallel()
	m := testMetrics(t)
	m.recordWiim("den", wifiReadings())

	m.forgetReceiver("den")

	body := scrape(t, m)
	requireNoSeries(t, body, "equipment_wiim_temperature_celsius")
	requireNoSeries(t, body, "equipment_wiim_remote_battery_ratio")
}

func TestRecordWiimOnANilMetricsIsANoOp(t *testing.T) {
	t.Parallel()
	var m *metrics
	m.recordWiim("den", wifiReadings())
}
