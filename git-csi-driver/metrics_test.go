package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// gaugeOf returns the value of the named gauge for the claim, and false
// when the gauge has no series for it.
func gaugeOf(t *testing.T, registry *metrics, name, namespace, claim string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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

// reported returns a volume with the claim, armed state, and number of
// pending paths that record reads.
func reported(claim claimReference, armed bool, pending int) *volume {
	vol := &volume{claim: claim, armed: armed}
	for range pending {
		vol.pending = append(vol.pending, change{path: "a.txt"})
	}
	return vol
}

func TestTheRuntimeCollectorsAreOnTheRegistry(t *testing.T) {
	recorder := httptest.NewRecorder()
	newMetrics().handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{"go_goroutines ", "process_start_time_seconds "} {
		if !strings.Contains(body, want) {
			t.Errorf("the scrape has no %s series", strings.TrimSpace(want))
		}
	}
}

func TestTheGaugesAreLabeledByTheClaim(t *testing.T) {
	registry := newMetrics()
	claim := claimReference{namespace: "home", name: "config"}
	registry.record(reported(claim, true, 3))

	armed, found := gaugeOf(t, registry, "git_csi_armed", "home", "config")
	if !found || armed != 1 {
		t.Errorf("git_csi_armed reads %v (found: %v), want 1", armed, found)
	}
	pending, found := gaugeOf(t, registry, "git_csi_pending_paths", "home", "config")
	if !found || pending != 3 {
		t.Errorf("git_csi_pending_paths reads %v (found: %v), want 3", pending, found)
	}

	registry.record(reported(claim, false, 0))
	if armed, _ := gaugeOf(t, registry, "git_csi_armed", "home", "config"); armed != 0 {
		t.Errorf("git_csi_armed reads %v after the class went, want 0", armed)
	}
}

func TestForgetDeletesTheVolumesGaugeSeries(t *testing.T) {
	registry := newMetrics()
	claim := claimReference{namespace: "home", name: "config"}
	vol := reported(claim, true, 1)
	registry.record(vol)
	registry.forget(vol)

	if _, found := gaugeOf(t, registry, "git_csi_armed", "home", "config"); found {
		t.Error("the claim is still on git_csi_armed after the volume went")
	}
	if _, found := gaugeOf(t, registry, "git_csi_pending_paths", "home", "config"); found {
		t.Error("the claim is still on git_csi_pending_paths after the volume went")
	}
}

// abnormalOf returns git_csi_volume_abnormal for the volume, and false
// when the gauge has no series for it.
func abnormalOf(t *testing.T, registry *metrics, namespace, id string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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

// mounted returns a volume of the given kind. The pod and the claim are
// in different namespaces, because the health gauge takes the pod's
// namespace for an inline volume and the claim's for any other.
func mounted(id string, kind volumeKind) *volume {
	vol := &volume{
		id:         id,
		attributes: &attributes{ref: "main"},
		kind:       kind,
		pod:        podReference{name: "reader", namespace: "home"},
	}
	if kind != inlineVolume {
		vol.claim = claimReference{namespace: "apps", name: "config"}
	}
	return vol
}

func TestTheHealthGaugeIsLabeledByNamespaceAndVolume(t *testing.T) {
	registry := newMetrics()
	inline := mounted("csi-1", inlineVolume)
	persistent := mounted("csi-2", writeableVolume)
	registry.health(inline, true)
	registry.health(persistent, false)

	if abnormal, found := abnormalOf(t, registry, "home", "csi-1"); !found || abnormal != 1 {
		t.Errorf("git_csi_volume_abnormal reads %v (found: %v) for the inline volume, want 1",
			abnormal, found)
	}
	if abnormal, found := abnormalOf(t, registry, "apps", "csi-2"); !found || abnormal != 0 {
		t.Errorf("git_csi_volume_abnormal reads %v (found: %v) for the claim's volume, want 0",
			abnormal, found)
	}

	registry.forget(inline)
	if _, found := abnormalOf(t, registry, "home", "csi-1"); found {
		t.Error("the volume is still on git_csi_volume_abnormal after it went")
	}
}

func TestTheDemandedPullsCounterIsLabeledByNamespaceAndVolume(t *testing.T) {
	registry := newMetrics()
	vol := mounted("csi-2", readOnlyClaim)
	registry.demanded(vol)
	registry.demanded(vol)

	count, found := demandedOf(t, registry, "apps", "csi-2")
	if !found || count != 2 {
		t.Errorf("git_csi_demanded_pulls_total reads %v (found: %v), want 2", count, found)
	}
	registry.forget(vol)
	if _, found := demandedOf(t, registry, "apps", "csi-2"); found {
		t.Error("the volume is still on git_csi_demanded_pulls_total after it went")
	}
}

func TestTheLogRecordsWhenAVolumeBecomesAbnormalAndWhenItBecomesNormal(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	vol := mounted("csi-1", inlineVolume)

	n.recordHealth(t.Context(), vol)
	if strings.Contains(logs.String(), "volume became") {
		t.Errorf("the log is %q, want no line for a volume that was well all along", logs)
	}

	vol.reportFetchFailed("the forge sent nothing")
	n.recordHealth(t.Context(), vol)
	n.recordHealth(t.Context(), vol)
	if got := strings.Count(logs.String(), "volume became abnormal"); got != 1 {
		t.Errorf("the log has the volume became abnormal %d times, want 1", got)
	}
	if !strings.Contains(logs.String(), "the forge sent nothing") {
		t.Errorf("the log is %q, want the report in it", logs)
	}
	if abnormal, _ := abnormalOf(t, n.metrics, "home", "csi-1"); abnormal != 1 {
		t.Errorf("git_csi_volume_abnormal reads %v while the fetch fails, want 1", abnormal)
	}

	vol.reportCommit("0123456789")
	n.recordHealth(t.Context(), vol)
	n.recordHealth(t.Context(), vol)
	if got := strings.Count(logs.String(), "volume became normal"); got != 1 {
		t.Errorf("the log has the volume became normal %d times, want 1", got)
	}
	if abnormal, _ := abnormalOf(t, n.metrics, "home", "csi-1"); abnormal != 0 {
		t.Errorf("git_csi_volume_abnormal reads %v after a fetch that worked, want 0", abnormal)
	}
}

func TestAVolumeWithNoClaimIsOnNoGauge(t *testing.T) {
	registry := newMetrics()
	vol := reported(claimReference{}, true, 1)
	registry.record(vol)
	registry.health(vol, true)
	registry.demanded(vol)
	registry.forget(vol)

	families, err := registry.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		// The runtime series and the build info are never empty. The
		// test checks only the series of the volume.
		if strings.HasPrefix(family.GetName(), "go_") || strings.HasPrefix(family.GetName(), "process_") ||
			family.GetName() == "liken_build_info" {
			continue
		}
		if len(family.GetMetric()) != 0 {
			t.Errorf("%s has %v, want nothing", family.GetName(), family.GetMetric())
		}
	}

	var absent *metrics
	absent.record(vol)
	absent.health(vol, true)
	absent.demanded(vol)
	absent.forget(vol)
	absent.watchRestarted(persistentVolumeKind)
	ran := false
	if err := absent.timeFetch("repo", func() error { ran = true; return nil }); err != nil {
		t.Errorf("timeFetch on a nil registry returned %v, want no error", err)
	}
	if !ran {
		t.Error("timeFetch on a nil registry did not run the fetch")
	}
}

func TestTheListenerServesTheGauges(t *testing.T) {
	registry := newMetrics()
	registry.record(reported(claimReference{namespace: "home", name: "config"}, true, 2))
	listener, err := registry.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan struct{})
	go func() {
		defer close(served)
		serveMetrics(ctx, listener, registry, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	response, err := http.Get("http://" + listener.Addr().String() + "/metrics")
	if err != nil {
		t.Fatalf("reading the metrics: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the metrics: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Errorf("the listener responded %d, want 200", response.StatusCode)
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
		t.Errorf("listen returned %v, %v, want no listener and no error", listener, err)
	}
}

func TestAListenerThatStopsOnItsOwnIsReported(t *testing.T) {
	logs := &logbook{}
	registry := newMetrics()
	listener, err := registry.listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening the metrics listener: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("closing the listener: %v", err)
	}
	serveMetrics(t.Context(), listener, registry, slog.New(slog.NewTextHandler(logs, nil)))
	if !strings.Contains(logs.String(), "The metrics listener stopped") {
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
		t.Error("newServer returned no error for an address it cannot take")
	}
}

func TestAVolumeWithNoClaimCountsNoPushFailure(t *testing.T) {
	registry := newMetrics()
	registry.pushFailed(&volume{})
	if _, found := counterOf(t, registry, "git_csi_push_failures_total", "", ""); found {
		t.Error("a volume with no claim is on the counter")
	}
}

// buildInfoOf returns liken_build_info for the version, and false when
// the version has not been reported.
func buildInfoOf(t *testing.T, registry *metrics, version string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
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
	registry := newMetrics()
	registry.reportBuildInfo("2026.09.10-001")

	if value, found := buildInfoOf(t, registry, "2026.09.10-001"); !found || value != 1 {
		t.Errorf("liken_build_info reads %v (found: %v), want 1", value, found)
	}
}

// callCounters returns, for the CSI call, the sample count of
// git_csi_reconcile_duration_seconds and git_csi_reconcile_errors_total.
func callCounters(t *testing.T, registry *metrics, kind string) (observations uint64, errs float64) {
	t.Helper()
	families, err := registry.registry.Gather()
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
			case "git_csi_reconcile_duration_seconds":
				observations = metric.GetHistogram().GetSampleCount()
			case "git_csi_reconcile_errors_total":
				errs = metric.GetCounter().GetValue()
			}
		}
	}
	return observations, errs
}

func TestObserveCallRecordsTheDurationAndCountsAnError(t *testing.T) {
	registry := newMetrics()
	registry.observeCall("NodePublishVolume", 10*time.Millisecond, false)
	registry.observeCall("NodePublishVolume", 20*time.Millisecond, true)

	observations, errs := callCounters(t, registry, "NodePublishVolume")
	if observations != 2 {
		t.Errorf("git_csi_reconcile_duration_seconds observed %d calls, want 2", observations)
	}
	if errs != 1 {
		t.Errorf("git_csi_reconcile_errors_total reads %v, want 1", errs)
	}
}

// gitCSIVolumesOf returns git_csi_volumes for the repository, and false
// when the metric has no series for it.
func gitCSIVolumesOf(t *testing.T, registry *metrics, repo string) (float64, bool) {
	t.Helper()
	families, err := registry.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_volumes" {
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

func TestGitCSIVolumesCountsByRepository(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	repo := n.store.repository(url).name

	first := publishRequest(t, "csi-1", url, map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), first); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	second := publishRequest(t, "csi-2", url, map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), second); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}
	if count, found := gitCSIVolumesOf(t, n.metrics, repo); !found || count != 2 {
		t.Errorf("git_csi_volumes reads %v (found: %v), want 2", count, found)
	}

	if _, err := n.NodeUnpublishVolume(t.Context(), &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csi-1", TargetPath: first.TargetPath,
	}); err != nil {
		t.Fatalf("NodeUnpublishVolume: %v", err)
	}
	if count, found := gitCSIVolumesOf(t, n.metrics, repo); !found || count != 1 {
		t.Errorf("git_csi_volumes reads %v (found: %v) after one unpublish, want 1", count, found)
	}
}

// fetchCounters returns, for the repository, the sample count of
// git_csi_fetch_duration_seconds and git_csi_fetch_failures_total.
func fetchCounters(t *testing.T, registry *metrics, repo string) (observations uint64, failures float64) {
	t.Helper()
	families, err := registry.registry.Gather()
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
			case "git_csi_fetch_duration_seconds":
				observations = metric.GetHistogram().GetSampleCount()
			case "git_csi_fetch_failures_total":
				failures = metric.GetCounter().GetValue()
			}
		}
	}
	return observations, failures
}

func TestTimeFetchObservesTheDurationAndCountsAFailure(t *testing.T) {
	registry := newMetrics()
	registry.registerNodeFacts(func() map[string]float64 { return nil })
	if err := registry.timeFetch("repo-1", func() error { return nil }); err != nil {
		t.Fatalf("timeFetch: %v", err)
	}
	refused := errors.New("the forge refused the fetch")
	if err := registry.timeFetch("repo-1", func() error { return refused }); !errors.Is(err, refused) {
		t.Errorf("timeFetch returned %v, want %v", err, refused)
	}

	observations, failures := fetchCounters(t, registry, "repo-1")
	if observations != 2 {
		t.Errorf("git_csi_fetch_duration_seconds observed %d fetches, want 2", observations)
	}
	if failures != 1 {
		t.Errorf("git_csi_fetch_failures_total reads %v, want 1", failures)
	}
}

// storeBytesOf returns git_csi_store_bytes.
func storeBytesOf(t *testing.T, registry *metrics) float64 {
	t.Helper()
	families, err := registry.registry.Gather()
	if err != nil {
		t.Fatalf("gathering the metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "git_csi_store_bytes" {
			continue
		}
		return family.GetMetric()[0].GetGauge().GetValue()
	}
	t.Fatal("git_csi_store_bytes is not on the registry")
	return 0
}

func TestMeasureStoreSetsGitCSIStoreBytes(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one two three"})
	request := publishRequest(t, "csi-1", fileURL(source), map[string]string{"pull": "never"})
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume: %v", err)
	}

	n.measureStore(t.Context())

	if got := storeBytesOf(t, n.metrics); got <= 0 {
		t.Errorf("git_csi_store_bytes reads %v, want more than 0 with a tree checked out", got)
	}
}

func TestMeasureStoreReportsAWalkItCannotRead(t *testing.T) {
	logs := &logbook{}
	n, _ := testNode(t, logs)
	n.store.root = filepath.Join(t.TempDir(), "gone")

	n.measureStore(t.Context())

	if !strings.Contains(logs.String(), "the store was not measured") {
		t.Errorf("the log is %q, want the the store measurement warning in it", logs)
	}
}
