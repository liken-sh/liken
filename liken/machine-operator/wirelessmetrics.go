package main

// The radios' signal metrics, read at scrape time.
//
// A radio's signal level and transmit bitrate change all the time, so
// they stay out of status: a status write for each change would cost a
// raft round on every leader. The supplicant reads both from the
// driver when it answers SIGNAL_POLL, and the driver measures them on
// the frames the radio already receives, so a request sends nothing
// over the air. This collector asks each radio's supplicant only when
// Prometheus scrapes, the same way the clock's collector reads its
// reading (clockmetrics.go). A machine that nobody scrapes sends no
// request, and no timer reads the signal.

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

// wirelessAskPatience bounds each request to a supplicant, so a
// supplicant that is restarting delays a scrape by at most this long.
const wirelessAskPatience = time.Second

// wirelessCollector asks every supplicant under dir at each scrape.
type wirelessCollector struct {
	dir string
}

func (c wirelessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- wirelessSignalDesc
	ch <- wirelessBitrateDesc
}

// Collect finds the radios by their control sockets, so a machine with
// no radio has no socket and publishes nothing. A radio that is not
// associated, or a supplicant that does not answer, publishes nothing
// for that radio, because a zero would read as a measurement.
func (c wirelessCollector) Collect(ch chan<- prometheus.Metric) {
	sockets, _ := filepath.Glob(machine.SupplicantSocket(c.dir, "*"))
	for _, socket := range sockets {
		ifname := filepath.Base(socket)
		association, err := machine.AskSupplicantAssociation(socket, wirelessAskPatience)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the signal of %s: %v\n", ifname, err)
			continue
		}
		if association.BSSID == "" {
			continue
		}
		poll, err := machine.AskSupplicant(socket, "SIGNAL_POLL", wirelessAskPatience)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reading the signal of %s: %v\n", ifname, err)
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
