package main

// displaymetrics.go holds this operator's own signals, the table plan
// 14 states. Every recording method is nil-safe, because most of the
// tests in this repository drive a pass or a prepare with no listener
// behind it, the way they drive one with no compositor behind it.
//
// Every method here reads a fact a pass already computed for a
// Display's status, a ResourceSlice's taint, or the layout module's
// report. None of them opens a card, a socket, or a wire of its own.

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newDisplayMetrics declares this operator's own series. newMetrics
// calls it once, on the same registry the runtime and the reconcile
// loop's own series share, so one scrape answers for all three layers.
func (m *metrics) newDisplayMetrics() {
	m.outputConnected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_connected",
		Help: "1 while a monitor is on the connector's wire, 0 while none is.",
	}, []string{"output"})
	m.outputClaimed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_claimed",
		Help: "1 while a prepared claim holds the output, 0 while none does.",
	}, []string{"output"})
	m.observationValid = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_observation_valid",
		Help: "1 while the source last answered, 0 while it did not.",
	}, []string{"source"})
	m.observationSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_observation_last_success_timestamp_seconds",
		Help: "When the source last answered, in seconds since the epoch.",
	}, []string{"source"})
	m.outputMode = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_mode_info",
		Help: "Always 1. The mode and refresh label the mode actuated on the output.",
	}, []string{"output", "mode", "refresh"})
	m.compositorRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "display_compositor_restarts_total",
		Help: "Times this operator ended the compositor, by why.",
	}, []string{"reason"})
	m.surfaces = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_surfaces",
		Help: "Surfaces the compositor holds on the output.",
	}, []string{"output"})
	m.panelPower = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_panel_power",
		Help: "1 while the panel's last DDC/CI reading reports on, 0 while it reports off or standby.",
	}, []string{"output"})
	m.panelBrightness = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_panel_brightness",
		Help: "The panel's last DDC/CI brightness reading, in the panel's own units.",
	}, []string{"output"})
}

// recordOutputs states the hardware triple's first two facts for
// every connector the card has: whether a monitor is on the wire, and
// whether a prepared claim holds the device. held carries the same
// answer preparedOutputs gives the Display controller's canvas heal,
// keyed by the device name a claim allocates, so a claim that holds no
// output known to sysfs answers false rather than nothing.
//
// The gauges are reset first, so an output the card stopped reporting
// leaves both series rather than reporting a fact from a walk that is
// no longer true. The card registers a connector for as long as the
// driver binds it, so the reset only ever drops a card that left with
// the node.
func (m *metrics) recordOutputs(outputs []Output, held map[string]bool) {
	if m == nil {
		return
	}
	m.outputConnected.Reset()
	m.outputClaimed.Reset()
	m.outputMode.Reset()
	for _, output := range outputs {
		m.outputConnected.WithLabelValues(output.Connector).Set(boolValue(output.Connected))
		m.outputClaimed.WithLabelValues(output.Connector).Set(boolValue(held[deviceName(output.Connector)]))
		m.recordMode(output.Connector, output.CurrentMode)
	}
}

// recordMode states the mode actuated on one output. A blank mode,
// which is an output driving nothing, states nothing: an info gauge
// with no known value is an absent series, not a zero one.
func (m *metrics) recordMode(output, current string) {
	if current == "" {
		return
	}
	parsed, err := parseMode(current)
	if err != nil {
		return
	}
	m.outputMode.WithLabelValues(output, parsed.Name, refreshLabel(parsed.Refresh)).Set(1)
}

// refreshLabel renders a mode's refresh the way every other label in
// this file renders a number: as a plain decimal, with no unit and no
// hertz suffix a dashboard would have to strip again.
func refreshLabel(hertz int) string {
	if hertz == 0 {
		return ""
	}
	return strconv.Itoa(hertz)
}

// recordObservation states whether one source answered on this pass,
// and the wall-clock time it last did. A source that fails keeps the
// gauges it already reported: display_observation_valid says the
// reading is stale, and the timestamp says how stale.
func (m *metrics) recordObservation(source string, ok bool, now time.Time) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(boolValue(ok))
	if ok {
		m.observationSuccess.WithLabelValues(source).Set(float64(now.Unix()))
	}
}

// compositorRestarted counts one restart this operator ordered. mode
// is a claim's prepare changing the resolution. heal is the canvas
// repair after the compositor re-creates an output. A third case is
// the compositor exiting on its own, between two passes. No call
// counts that case, because nothing today tells it apart from a
// restart this operator ordered.
func (m *metrics) compositorRestarted(reason string) {
	if m == nil {
		return
	}
	m.compositorRestarts.WithLabelValues(reason).Inc()
}

// recordSurfaces states how many surfaces the compositor holds on one
// output, from the same module report the placement pass just placed
// them from.
func (m *metrics) recordSurfaces(output string, count int) {
	if m == nil {
		return
	}
	m.surfaces.WithLabelValues(output).Set(float64(count))
}

// recordPanel states the panel's last DDC/CI reading for power and
// brightness, from the same facts a pass just wrote into the Display's
// status.observed. A control the panel never answered leaves its gauge
// unset, because a reading this operator never took is not a zero.
func (m *metrics) recordPanel(output string, facts panelFacts) {
	if m == nil {
		return
	}
	if power, known := facts.Observed[vcpPowerMode]; known {
		m.panelPower.WithLabelValues(output).Set(boolValue(power == powerModeOn))
	}
	if brightness, known := facts.Observed[vcpBrightness]; known {
		m.panelBrightness.WithLabelValues(output).Set(float64(brightness))
	}
}

// boolValue renders a state as the 1-or-0 milestone 65 states for
// every gauge in the hardware triple.
func boolValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}
