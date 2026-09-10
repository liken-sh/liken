package main

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordOutputsStatesConnectedAndClaimed(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	outputs := []Output{
		{Connector: "HDMI-A-1", Connected: true},
		{Connector: "DP-1", Connected: false},
	}
	held := map[string]bool{"hdmi-a-1": true}
	readings.recordOutputs(outputs, held)

	if got := testutil.ToFloat64(readings.outputConnected.WithLabelValues("HDMI-A-1")); got != 1 {
		t.Errorf("display_output_connected{output=\"HDMI-A-1\"} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.outputConnected.WithLabelValues("DP-1")); got != 0 {
		t.Errorf("display_output_connected{output=\"DP-1\"} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(readings.outputClaimed.WithLabelValues("HDMI-A-1")); got != 1 {
		t.Errorf("display_output_claimed{output=\"HDMI-A-1\"} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.outputClaimed.WithLabelValues("DP-1")); got != 0 {
		t.Errorf("display_output_claimed{output=\"DP-1\"} = %v, want 0", got)
	}
}

func TestRecordOutputsForgetsAConnectorTheCardStoppedReporting(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.recordOutputs([]Output{{Connector: "HDMI-A-1", Connected: true}}, nil)
	readings.recordOutputs([]Output{{Connector: "DP-1", Connected: true}}, nil)

	if count := testutil.CollectAndCount(readings.outputConnected); count != 1 {
		t.Errorf("display_output_connected carries %d series, want 1", count)
	}
}

func TestRecordModeStatesTheActuatedModeAndRefresh(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.recordOutputs([]Output{{Connector: "HDMI-A-1", Connected: true, CurrentMode: "3840x1600@24"}}, nil)

	got := testutil.ToFloat64(readings.outputMode.WithLabelValues("HDMI-A-1", "3840x1600", "24"))
	if got != 1 {
		t.Errorf("display_output_mode_info{output=\"HDMI-A-1\",mode=\"3840x1600\",refresh=\"24\"} = %v, want 1", got)
	}
}

func TestRecordModeStatesNothingForAnOutputDrivingNothing(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.recordOutputs([]Output{{Connector: "HDMI-A-1", Connected: false, CurrentMode: ""}}, nil)

	if count := testutil.CollectAndCount(readings.outputMode); count != 0 {
		t.Errorf("display_output_mode_info carries %d series, want none", count)
	}
}

func TestRecordObservationStatesValidityAndTheSuccessTimestamp(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	readings.recordObservation("card", true, now)
	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues("card")); got != 1 {
		t.Errorf("display_observation_valid{source=\"card\"} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.observationSuccess.WithLabelValues("card")); got != float64(now.Unix()) {
		t.Errorf("display_observation_last_success_timestamp_seconds{source=\"card\"} = %v, want %v", got, now.Unix())
	}
}

func TestRecordObservationOnFailureKeepsTheLastSuccessTimestamp(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	first := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	readings.recordObservation("compositor", true, first)

	later := first.Add(time.Minute)
	readings.recordObservation("compositor", false, later)

	if got := testutil.ToFloat64(readings.observationValid.WithLabelValues("compositor")); got != 0 {
		t.Errorf("display_observation_valid{source=\"compositor\"} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(readings.observationSuccess.WithLabelValues("compositor")); got != float64(first.Unix()) {
		t.Errorf("the timestamp moved on a failed observation: got %v, want the last success at %v", got, first.Unix())
	}
}

func TestCompositorRestartedCountsByReason(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.compositorRestarted("mode")
	readings.compositorRestarted("mode")
	readings.compositorRestarted("heal")

	if got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("mode")); got != 2 {
		t.Errorf("display_compositor_restarts_total{reason=\"mode\"} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(readings.compositorRestarts.WithLabelValues("heal")); got != 1 {
		t.Errorf("display_compositor_restarts_total{reason=\"heal\"} = %v, want 1", got)
	}
}

func TestARepeatedScrapeLeavesTheRestartCounterUnchanged(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.compositorRestarted("mode")

	first := scrape(t, readings)
	second := scrape(t, readings)
	want := `display_compositor_restarts_total{reason="mode"} 1`
	if !strings.Contains(first, want) || !strings.Contains(second, want) {
		t.Errorf("the restart count moved between two scrapes that recorded nothing new")
	}
}

func TestRecordSurfacesStatesTheCountPerOutput(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.recordSurfaces("HDMI-A-1", 3)
	readings.recordSurfaces("DP-1", 0)

	if got := testutil.ToFloat64(readings.surfaces.WithLabelValues("HDMI-A-1")); got != 3 {
		t.Errorf("display_surfaces{output=\"HDMI-A-1\"} = %v, want 3", got)
	}
	if got := testutil.ToFloat64(readings.surfaces.WithLabelValues("DP-1")); got != 0 {
		t.Errorf("display_surfaces{output=\"DP-1\"} = %v, want 0", got)
	}
}

func TestRecordPanelStatesPowerAndBrightnessWhenKnown(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	facts := panelFacts{Observed: map[byte]uint16{vcpPowerMode: powerModeOn, vcpBrightness: 80}}
	readings.recordPanel("HDMI-A-1", facts)

	if got := testutil.ToFloat64(readings.panelPower.WithLabelValues("HDMI-A-1")); got != 1 {
		t.Errorf("display_panel_power{output=\"HDMI-A-1\"} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(readings.panelBrightness.WithLabelValues("HDMI-A-1")); got != 80 {
		t.Errorf("display_panel_brightness{output=\"HDMI-A-1\"} = %v, want 80", got)
	}
}

func TestRecordPanelStatesNothingForAControlNeverRead(t *testing.T) {
	readings := newMetrics("display-operator", "dev")
	readings.recordPanel("HDMI-A-1", panelFacts{})

	if count := testutil.CollectAndCount(readings.panelPower); count != 0 {
		t.Errorf("display_panel_power carries %d series, want none", count)
	}
	if count := testutil.CollectAndCount(readings.panelBrightness); count != 0 {
		t.Errorf("display_panel_brightness carries %d series, want none", count)
	}
}

func TestEveryDisplayMetricRecorderIsNilSafe(t *testing.T) {
	// Every test in this repository that drives a pass with no
	// listener behind it drives one with a nil metrics too, so a
	// pass must record nothing rather than panic.
	var readings *metrics
	readings.recordOutputs([]Output{{Connector: "HDMI-A-1", Connected: true}}, nil)
	readings.recordObservation("card", true, time.Now())
	readings.compositorRestarted("mode")
	readings.recordSurfaces("HDMI-A-1", 1)
	readings.recordPanel("HDMI-A-1", panelFacts{Observed: map[byte]uint16{vcpPowerMode: powerModeOn}})
}
