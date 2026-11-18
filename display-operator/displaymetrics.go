package main

// This file defines the display-specific metrics and the methods that
// record them. Every recording method does nothing on a nil *metrics,
// because most tests drive a pass or a prepare with no metrics behind
// it.
//
// Each method records a value that a pass already computed for a
// Display's status, a ResourceSlice taint, or the layout module's
// report. None of them reads the card, a socket, or a monitor.

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// newDisplayMetrics creates the display series. newMetrics registers
// them on the same registry as the process and reconcile-loop series.
func (m *metrics) newDisplayMetrics() {
	m.outputConnected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_connected",
		Help: "1 while a monitor is connected to the output, 0 while none is.",
	}, []string{"output"})
	m.outputClaimed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_claimed",
		Help: "1 while a prepared claim holds the output, 0 while none does.",
	}, []string{"output"})
	m.observationValid = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_observation_valid",
		Help: "1 while the last read of the source succeeded, 0 while it failed.",
	}, []string{"source"})
	m.observationSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_observation_last_success_timestamp_seconds",
		Help: "When the last read of the source succeeded, in seconds since the epoch.",
	}, []string{"source"})
	m.outputMode = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_output_mode_info",
		Help: "Always 1. The mode and refresh labels give the mode that the operator set on the output.",
	}, []string{"output", "mode", "refresh"})
	m.compositorRestarts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "display_compositor_restarts_total",
		Help: "Times this operator ended the compositor, by reason.",
	}, []string{"reason"})
	m.compositorServing = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "display_compositor_serving",
		Help: "1 while the compositor replies on its Wayland socket, 0 while the socket refuses the connection or the compositor does not reply.",
	})
	m.compositorContainerRestarts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "display_compositor_container_restarts_total",
		Help: "Times the kubelet started the compositor's container again after a crash, from this pod's own status.",
	})
	m.surfaces = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_surfaces",
		Help: "Surfaces the compositor has on the output.",
	}, []string{"output"})
	m.panelPower = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_panel_power",
		Help: "1 while the panel's last DDC/CI reading reports on, 0 while it reports off or standby.",
	}, []string{"output"})
	m.panelBrightness = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "display_panel_brightness",
		Help: "The panel's last DDC/CI brightness reading, in the units that the panel reports.",
	}, []string{"output"})
}

// recordOutputs sets the connected, claimed, and mode series for every
// connector on the card. prepared holds the output devices from
// preparedOutputs, keyed by device name. It resets the series first,
// so a connector that the card no longer reports does not keep its
// last value.
func (m *metrics) recordOutputs(outputs []Output, prepared map[string]bool) {
	if m == nil {
		return
	}
	m.outputConnected.Reset()
	m.outputClaimed.Reset()
	m.outputMode.Reset()
	for _, output := range outputs {
		m.outputConnected.WithLabelValues(output.Connector).Set(boolValue(output.Connected))
		m.outputClaimed.WithLabelValues(output.Connector).Set(boolValue(prepared[deviceName(output.Connector)]))
		m.recordMode(output.Connector, output.CurrentMode)
	}
}

// recordMode sets the mode series for one output. An output with no
// current mode, or one that does not parse, gets no series.
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

// refreshLabel formats a refresh rate in hertz with no unit, or returns
// "" when the mode has none.
func refreshLabel(hertz int) string {
	if hertz == 0 {
		return ""
	}
	return strconv.Itoa(hertz)
}

// recordObservation records whether a reading of one source succeeded
// on this pass. A failure sets display_observation_valid to 0 and
// leaves the success timestamp alone, so the timestamp shows the age
// of the last good reading.
func (m *metrics) recordObservation(source string, ok bool, now time.Time) {
	if m == nil {
		return
	}
	m.observationValid.WithLabelValues(source).Set(boolValue(ok))
	if ok {
		m.observationSuccess.WithLabelValues(source).Set(float64(now.Unix()))
	}
}

// compositorRestarted counts one compositor restart that this operator
// ordered, by reason:
//   - mode: a claim's prepare changed the resolution.
//   - heal: the canvas was repaired after the compositor recreated an
//     output.
//   - hung: the socket watch killed a compositor whose probe read Hung
//     for compositorHungLimit.
//   - masterless: the card gate reported a compositor with no DRM
//     master.
//
// A compositor that exits on its own is counted by
// display_compositor_container_restarts_total instead, from the
// kubelet's restart count.
func (m *metrics) compositorRestarted(reason string) {
	if m == nil {
		return
	}
	m.compositorRestarts.WithLabelValues(reason).Inc()
}

// recordSurfaces sets the number of surfaces on one output, taken from
// the layout module report that the placement pass just used.
func (m *metrics) recordSurfaces(output string, count int) {
	if m == nil {
		return
	}
	m.surfaces.WithLabelValues(output).Set(float64(count))
}

// forgetSurfaces removes the surface count of every output. When no
// compositor serves, the last counts describe a compositor that is
// gone.
func (m *metrics) forgetSurfaces() {
	if m == nil {
		return
	}
	m.surfaces.Reset()
}

// recordCompositorServing records the result of the compositor socket
// probe, the same result that sets the taint on the ResourceSlice's
// outputs.
func (m *metrics) recordCompositorServing(serving bool) {
	if m == nil {
		return
	}
	m.compositorServing.Set(boolValue(serving))
}

// recordCompositorRestarts adds the container restarts since the last
// reading. The caller passes the growth, not the pod's total, because
// a new pod's restart count starts at zero.
func (m *metrics) recordCompositorRestarts(growth int) {
	if m == nil || growth <= 0 {
		return
	}
	m.compositorContainerRestarts.Add(float64(growth))
}

// recordPanel sets the panel power and brightness series from the same
// values the pass wrote to the Display's status.observed. A control the
// panel never reported leaves its series unset, because no reading is
// not a reading of zero.
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

// boolValue returns the gauge value of a boolean.
func boolValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}
