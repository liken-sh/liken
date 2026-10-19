package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/liken-sh/liken/liken/machine/supplicanttest"
)

// signalPoll is the answer a real wpa_supplicant 2.12 gave to
// SIGNAL_POLL on an associated radio.
const signalPoll = "RSSI=-71\nLINKSPEED=144\nNOISE=9999\nFREQUENCY=2437\nWIDTH=20 MHz\nCENTER_FRQ1=2437\nAVG_RSSI=-70\nAVG_BEACON_RSSI=-70\n"

// scrapeRadios scrapes a registry that holds only the radios'
// collector, over the supplicants under dir.
func scrapeRadios(t *testing.T, dir string) string {
	t.Helper()
	registry := prometheus.NewRegistry()
	registry.MustRegister(wirelessCollector{dir: dir})
	return scrapeHandler(t, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}

func TestTheWirelessMetricsCarryTheSignalOfTheAccessPoint(t *testing.T) {
	dir := t.TempDir()
	s := supplicanttest.Start(t, dir, "wlan0")
	s.Answer("STATUS", "bssid=04:4a:2c:44:55:66\nfreq=2437\nwpa_state=COMPLETED\n")
	s.Answer("SIGNAL_POLL", signalPoll)

	body := scrapeRadios(t, dir)
	requireSeries(t, body, `liken_wireless_signal_dbm{bssid="04:4a:2c:44:55:66",interface="wlan0"} -71`)
	requireSeries(t, body, `liken_wireless_tx_bitrate_bits_per_second{bssid="04:4a:2c:44:55:66",interface="wlan0"} 1.44e+08`)
}

func TestARadioThatIsNotAssociatedReportsNoSignal(t *testing.T) {
	dir := t.TempDir()
	s := supplicanttest.Start(t, dir, "wlan0")
	s.Answer("STATUS", "wpa_state=SCANNING\n")

	body := scrapeRadios(t, dir)
	requireNoSeries(t, body, "liken_wireless_signal_dbm")
	if asked := s.Asked(); len(asked) != 1 {
		t.Errorf("the collector asked %q of a radio with no access point", asked)
	}
}

func TestAMachineWithNoRadioReportsNoSignal(t *testing.T) {
	body := scrapeRadios(t, t.TempDir())
	requireNoSeries(t, body, "liken_wireless_signal_dbm")
	requireNoSeries(t, body, "liken_wireless_tx_bitrate_bits_per_second")
}

func TestASupplicantThatRefusesThePollReportsNoSignal(t *testing.T) {
	dir := t.TempDir()
	s := supplicanttest.Start(t, dir, "wlan0")
	s.Answer("STATUS", "bssid=04:4a:2c:44:55:66\nfreq=2437\nwpa_state=COMPLETED\n")
	s.Answer("SIGNAL_POLL", "FAIL\n")

	body := scrapeRadios(t, dir)
	requireNoSeries(t, body, "liken_wireless_signal_dbm")
}
