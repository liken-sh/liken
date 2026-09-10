package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
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
	absent.watchRestarted(persistentVolumeKind)
	ran := false
	if err := absent.timeFetch("repo", func() error { ran = true; return nil }); err != nil {
		t.Errorf("timeFetch on a nil registry answered %v, want no error", err)
	}
	if !ran {
		t.Error("timeFetch on a nil registry did not run the fetch")
	}
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

// buildInfoOf is what liken_build_info reads for the version, and false
// when the release has not been reported.
func buildInfoOf(t *testing.T, readings *metrics, version string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "liken_build_info" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["component"] == "git-csi-driver" && labels["version"] == version {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func TestReportBuildInfoSetsLikenBuildInfo(t *testing.T) {
	readings := newMetrics()
	readings.reportBuildInfo("2026.09.10-001")

	if value, found := buildInfoOf(t, readings, "2026.09.10-001"); !found || value != 1 {
		t.Errorf("liken_build_info reads %v (found: %v), want 1", value, found)
	}
}

// callCounters is what gitcsi_reconcile_duration_seconds and
// gitcsi_reconcile_errors_total read for the operation, and how many
// times duration was observed at all, which the histogram's own count
// carries.
func callCounters(t *testing.T, readings *metrics, kind string) (observations uint64, errs float64) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			matches := false
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					matches = true
				}
			}
			if !matches {
				continue
			}
			switch family.GetName() {
			case "gitcsi_reconcile_duration_seconds":
				observations = metric.GetHistogram().GetSampleCount()
			case "gitcsi_reconcile_errors_total":
				errs = metric.GetCounter().GetValue()
			}
		}
	}
	return observations, errs
}

func TestObserveCallRecordsTheDurationAndCountsAnError(t *testing.T) {
	readings := newMetrics()
	readings.observeCall("NodePublishVolume", 10*time.Millisecond, false)
	readings.observeCall("NodePublishVolume", 20*time.Millisecond, true)

	observations, errs := callCounters(t, readings, "NodePublishVolume")
	if observations != 2 {
		t.Errorf("gitcsi_reconcile_duration_seconds observed %d calls, want 2", observations)
	}
	if errs != 1 {
		t.Errorf("gitcsi_reconcile_errors_total reads %v, want 1", errs)
	}
}

// gitcsiVolumesOf is what gitcsi_volumes reads for the repository, and
// false when the repository is on no series.
func gitcsiVolumesOf(t *testing.T, readings *metrics, repo string) (float64, bool) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "gitcsi_volumes" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "repo" && label.GetValue() == repo {
					return metric.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func TestGitcsiVolumesCountsByRepository(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	repo := answering.store.repository(url).name

	first := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	second := publishRequest(t, "csi-2", url, map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), second); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if count, found := gitcsiVolumesOf(t, answering.readings, repo); !found || count != 2 {
		t.Errorf("gitcsi_volumes reads %v (found: %v), want 2", count, found)
	}

	if _, err := answering.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csi-1", TargetPath: first.TargetPath,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if count, found := gitcsiVolumesOf(t, answering.readings, repo); !found || count != 1 {
		t.Errorf("gitcsi_volumes reads %v (found: %v) after one unpublish, want 1", count, found)
	}
}

// fetchCounters is what gitcsi_fetch_duration_seconds and
// gitcsi_fetch_failures_total read for the repository.
func fetchCounters(t *testing.T, readings *metrics, repo string) (observations uint64, failures float64) {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			matches := false
			for _, label := range metric.GetLabel() {
				if label.GetName() == "repo" && label.GetValue() == repo {
					matches = true
				}
			}
			if !matches {
				continue
			}
			switch family.GetName() {
			case "gitcsi_fetch_duration_seconds":
				observations = metric.GetHistogram().GetSampleCount()
			case "gitcsi_fetch_failures_total":
				failures = metric.GetCounter().GetValue()
			}
		}
	}
	return observations, failures
}

func TestTimeFetchObservesTheDurationAndCountsAFailure(t *testing.T) {
	readings := newMetrics()
	readings.registerNodeFacts(func() map[string]float64 { return nil })
	if err := readings.timeFetch("repo-1", func() error { return nil }); err != nil {
		t.Fatalf("timeFetch: %v", err)
	}
	refused := errors.New("the forge refused the fetch")
	if err := readings.timeFetch("repo-1", func() error { return refused }); !errors.Is(err, refused) {
		t.Errorf("timeFetch answered %v, want %v", err, refused)
	}

	observations, failures := fetchCounters(t, readings, "repo-1")
	if observations != 2 {
		t.Errorf("gitcsi_fetch_duration_seconds observed %d fetches, want 2", observations)
	}
	if failures != 1 {
		t.Errorf("gitcsi_fetch_failures_total reads %v, want 1", failures)
	}
}

// storeBytesOf is what gitcsi_store_bytes reads.
func storeBytesOf(t *testing.T, readings *metrics) float64 {
	t.Helper()
	families, err := readings.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "gitcsi_store_bytes" {
			continue
		}
		return family.GetMetric()[0].GetGauge().GetValue()
	}
	t.Fatal("gitcsi_store_bytes is not on the registry")
	return 0
}

func TestMeasureStoreSetsGitcsiStoreBytes(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one two three"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	answering.measureStore(t.Context())

	if got := storeBytesOf(t, answering.readings); got <= 0 {
		t.Errorf("gitcsi_store_bytes reads %v, want more than 0 with a tree checked out", got)
	}
}

func TestMeasureStoreReportsAWalkItCannotRead(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.store.root = filepath.Join(t.TempDir(), "gone")

	answering.measureStore(t.Context())

	if !strings.Contains(logs.String(), "the store was not measured") {
		t.Errorf("the log is %q, want the store not measured in it", logs)
	}
}
