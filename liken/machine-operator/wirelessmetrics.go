package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

var (
	wirelessSignalDesc = prometheus.NewDesc(metrics.Prefix+"wireless_signal_dbm",
		"The signal level of the access point a radio is associated with, in dBm, as the driver last measured it. Absent while the radio is not associated.",
		[]string{"interface", "bssid"}, nil)
	wirelessBitrateDesc = prometheus.NewDesc(metrics.Prefix+"wireless_tx_bitrate_bits_per_second",
		"The bitrate a radio last sent at to its access point. Absent while the radio is not associated.",
		[]string{"interface", "bssid"}, nil)
)

// supplicantTimeout bounds each request to a supplicant, so a
// supplicant that is restarting delays a scrape by at most this long
// for each request.
const supplicantTimeout = time.Second

// wirelessCollector publishes each radio's signal level and transmit
// bitrate on each scrape. Both change constantly, so they are not in
// status, where each change would be a datastore write. Collect queries
// wpa_supplicant through each control socket in dir only during a
// scrape. The supplicant answers SIGNAL_POLL with what the driver
// measured on frames the radio already received, so a query sends
// nothing over the air.
type wirelessCollector struct {
	dir string
}

func (c wirelessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- wirelessSignalDesc
	ch <- wirelessBitrateDesc
}

// Collect publishes nothing for a radio that is not associated, or
// whose supplicant does not answer, because a zero would look like a
// measurement.
func (c wirelessCollector) Collect(ch chan<- prometheus.Metric) {
	sockets, _ := filepath.Glob(machine.SupplicantSocket(c.dir, "*"))
	for _, socket := range sockets {
		ifname := filepath.Base(socket)
		association, err := machine.AskSupplicantAssociation(socket, supplicantTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the association of wireless interface %s: %v\n", ifname, err)
			continue
		}
		if association.BSSID == "" {
			continue
		}
		poll, err := machine.AskSupplicant(socket, "SIGNAL_POLL", supplicantTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "polling the signal of wireless interface %s: %v\n", ifname, err)
			continue
		}
		if rssi, err := strconv.Atoi(poll["RSSI"]); err == nil {
			ch <- prometheus.MustNewConstMetric(wirelessSignalDesc, prometheus.GaugeValue,
				float64(rssi), ifname, association.BSSID)
		}
		// The supplicant reports LINKSPEED in Mbit/s.
		if mbps, err := strconv.Atoi(poll["LINKSPEED"]); err == nil {
			ch <- prometheus.MustNewConstMetric(wirelessBitrateDesc, prometheus.GaugeValue,
				float64(mbps)*1e6, ifname, association.BSSID)
		}
	}
}
