package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gaugeOf is what one gauge reads for the claim, and false when the claim
// is on no gauge.
func gaugeOf(t *testing.T, readings *metrics, name, namespace, claim string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["namespace"] == namespace && labels["claim"] == claim {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// reported is a volume that has been read once, which is what the gauges
// take.
func reported(claim claimReference, armed bool, pending int) *volume {
	held := &volume{claim: claim, armed: armed}
	for range pending {
		held.pending = append(held.pending, change{path: "a.txt"})
	}
	return held
}

func TestTheGaugesCarryTheClaim(t *testing.T) {
	readings := newMetrics()
	claim := claimReference{namespace: "home", name: "config"}
	readings.record(reported(claim, true, 3))

	armed, found := gaugeOf(t, readings, "git_csi_armed", "home", "config")
	if !found || armed != 1 {
		t.Errorf("git_csi_armed reads %v (found: %v), want 1", armed, found)
	}
	pending, found := gaugeOf(t, readings, "git_csi_pending_paths", "home", "config")
	if !found || pending != 3 {
		t.Errorf("git_csi_pending_paths reads %v (found: %v), want 3", pending, found)
	}

	readings.record(reported(claim, false, 0))
	if armed, _ := gaugeOf(t, readings, "git_csi_armed", "home", "config"); armed != 0 {
		t.Errorf("git_csi_armed reads %v after the class went, want 0", armed)
	}
}

func TestForgetTakesTheVolumeOffTheGauges(t *testing.T) {
	readings := newMetrics()
	claim := claimReference{namespace: "home", name: "config"}
	held := reported(claim, true, 1)
	readings.record(held)
	readings.forget(held)

	if _, found := gaugeOf(t, readings, "git_csi_armed", "home", "config"); found {
		t.Error("the claim is still on git_csi_armed after the volume went")
	}
	if _, found := gaugeOf(t, readings, "git_csi_pending_paths", "home", "config"); found {
		t.Error("the claim is still on git_csi_pending_paths after the volume went")
	}
}

// abnormalOf is what git_csi_volume_abnormal reads for the volume, and
// false when the volume is on no gauge.
func abnormalOf(t *testing.T, readings *metrics, namespace, id string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_volume_abnormal" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["namespace"] == namespace && labels["volume"] == id {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// mounted is a volume of each kind, which the health gauge labels
// from a different namespace: the pod's for the inline volume, and the
// claim's for the one a claim binds.
func mounted(id string, kind volumeKind) *volume {
	held := &volume{
		id:         id,
		attributes: &attributes{ref: "main"},
		kind:       kind,
		pod:        podReference{name: "reader", namespace: "home"},
	}
	if kind != inlineVolume {
		held.claim = claimReference{namespace: "apps", name: "config"}
	}
	return held
}

func TestTheHealthGaugeCarriesTheNamespaceAndTheVolume(t *testing.T) {
	readings := newMetrics()
	inline := mounted("csi-1", inlineVolume)
	persistent := mounted("csi-2", writeableVolume)
	readings.health(inline, true)
	readings.health(persistent, false)

	if abnormal, found := abnormalOf(t, readings, "home", "csi-1"); !found || abnormal != 1 {
		t.Errorf("git_csi_volume_abnormal reads %v (found: %v) for the inline volume, want 1",
			abnormal, found)
	}
	if abnormal, found := abnormalOf(t, readings, "apps", "csi-2"); !found || abnormal != 0 {
		t.Errorf("git_csi_volume_abnormal reads %v (found: %v) for the claim's volume, want 0",
			abnormal, found)
	}

	readings.forget(inline)
	if _, found := abnormalOf(t, readings, "home", "csi-1"); found {
		t.Error("the volume is still on git_csi_volume_abnormal after it went")
	}
}

func TestTheDemandedPullsCounterCarriesTheNamespaceAndTheVolume(t *testing.T) {
	readings := newMetrics()
	held := mounted("csi-2", readOnlyClaim)
	readings.demanded(held)
	readings.demanded(held)

	counted, found := demandedOf(t, readings, "apps", "csi-2")
	if !found || counted != 2 {
		t.Errorf("git_csi_demanded_pulls_total reads %v (found: %v), want 2", counted, found)
	}
	readings.forget(held)
	if _, found := demandedOf(t, readings, "apps", "csi-2"); found {
		t.Error("the volume is still on git_csi_demanded_pulls_total after it went")
	}
}

func TestTheLogSaysWhenAVolumeTurnsAbnormalAndWhenItIsWellAgain(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	held := mounted("csi-1", inlineVolume)

	answering.noteHealth(t.Context(), held)
	if strings.Contains(logs.String(), "the volume is") {
		t.Errorf("the log is %q, want no line for a volume that was well all along", logs)
	}

	held.reportTrouble("the forge answered nothing")
	answering.noteHealth(t.Context(), held)
	answering.noteHealth(t.Context(), held)
	if got := strings.Count(logs.String(), "the volume is abnormal"); got != 1 {
		t.Errorf("the log says the volume is abnormal %d times, want 1", got)
	}
	if !strings.Contains(logs.String(), "the forge answered nothing") {
		t.Errorf("the log is %q, want the report in it", logs)
	}
	if abnormal, _ := abnormalOf(t, answering.readings, "home", "csi-1"); abnormal != 1 {
		t.Errorf("git_csi_volume_abnormal reads %v while the fetch fails, want 1", abnormal)
	}

	held.reportCommit("0123456789")
	answering.noteHealth(t.Context(), held)
	answering.noteHealth(t.Context(), held)
	if got := strings.Count(logs.String(), "the volume is normal"); got != 1 {
		t.Errorf("the log says the volume is normal %d times, want 1", got)
	}
	if abnormal, _ := abnormalOf(t, answering.readings, "home", "csi-1"); abnormal != 0 {
		t.Errorf("git_csi_volume_abnormal reads %v after a fetch that worked, want 0", abnormal)
	}
}

func TestAVolumeWithNoClaimIsOnNoGauge(t *testing.T) {
	readings := newMetrics()
	held := reported(claimReference{}, true, 1)
	readings.record(held)
	readings.health(held, true)
	readings.demanded(held)
	readings.forget(held)

	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if len(family.GetMetric()) != 0 {
			t.Errorf("%s carries %v, want nothing", family.GetName(), family.GetMetric())
		}
	}

	var absent *metrics
	absent.record(held)
	absent.health(held, true)
	absent.demanded(held)
	absent.forget(held)
}

func TestTheListenerServesTheGauges(t *testing.T) {
	readings := newMetrics()
	readings.record(reported(claimReference{namespace: "home", name: "config"}, true, 2))
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() {
		defer close(served)
		serveMetrics(ctx, listener, readings, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	answer, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("reading the metrics: %v", err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("reading the metrics: %v", err)
	}
	if answer.StatusCode != http.StatusOK {
		t.Errorf("the listener answered %d, want 200", answer.StatusCode)
	}
	if !strings.Contains(string(body), `git_csi_armed{claim="config",namespace="home"} 1`) {
		t.Errorf("the metrics are %s, want the volume on them", body)
	}

	stop()
	select {
	case <-served:
	case <-time.After(30 * time.Second):
		t.Fatal("the listener did not stop with the run")
	}
}

func TestAnEmptyAddressServesNoMetrics(t *testing.T) {
	listener, err := newMetrics().listen("")
	if err != nil || listener != nil {
		t.Errorf("listen answered %v, %v, want no listener and no error", listener, err)
	}
}

func TestAListenerThatStopsOnItsOwnIsReported(t *testing.T) {
	logs := &logbook{}
	readings := newMetrics()
	listener, err := readings.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	serveMetrics(t.Context(), listener, readings, slog.New(slog.NewTextHandler(logs, nil)))
	if !strings.Contains(logs.String(), "the metrics listener stopped") {
		t.Errorf("the log is %q, want the listener that stopped in it", logs)
	}
}

func TestTheDriverServesTheMetricsItIsGivenAnAddressFor(t *testing.T) {
	dir := t.TempDir()
	cfg := &config{
		endpoint: "unix://" + filepath.Join(dir, "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(dir, "store"),
		metrics:  "127.0.0.1:0",
	}
	start(t, cfg, io.Discard)

	_, err := newServer(t.Context(), &config{
		endpoint: "unix://" + filepath.Join(t.TempDir(), "csi.sock"),
		nodeID:   "node-1",
		store:    filepath.Join(t.TempDir(), "store"),
		metrics:  "127.0.0.1:-1",
	}, slog.Default())
	if err == nil {
		t.Error("newServer answered no error for an address it cannot take")
	}
}

func TestAVolumeWithNoClaimCountsNoPushFailure(t *testing.T) {
	readings := newMetrics()
	readings.pushFailed(&volume{})
	if _, found := counterOf(t, readings, "git_csi_push_failures_total", "", ""); found {
		t.Error("a volume with no claim is on the counter")
	}
}
