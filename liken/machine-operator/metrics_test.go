package main

// These tests read layer 3 the way Prometheus does: they scrape a real
// registry through its HTTP handler and parse the text that comes back.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/kubernetes"
	"github.com/liken-sh/liken/liken/machine"
)

// observer builds an operator registry with layer 3 on it. It returns
// the layer and a function that scrapes the registry. The listener is
// off, because the tests call the handler directly.
func observer(t *testing.T, f *fetcher) (*machineMetrics, func() string) {
	t.Helper()
	o, layer := serveMetrics("", f, nil)
	return layer, func() string { return scrapeHandler(t, o.Handler()) }
}

// scrapeHandler makes one HTTP GET against the handler and returns the
// response body.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("requesting /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the scrape: %v", err)
	}
	return string(body)
}

func TestTheListenerStartsWithThisMachinesMetrics(t *testing.T) {
	o, layer := serveMetrics("127.0.0.1:0", &fetcher{}, nil)
	if o == nil || layer == nil {
		t.Fatalf("serveMetrics returned %v and %v, want an operator and a layer", o, layer)
	}
	body := scrapeHandler(t, o.Handler())
	requireSeries(t, body, `liken_machine_change_pending{tier="reboot"} 0`)
	if len(series(body, "liken_build_info")) != 1 {
		t.Errorf("liken_build_info is %q, want one series", series(body, "liken_build_info"))
	}
}

// series returns the sample lines of the metric called name, so a test
// compares values and not help text.
func series(body, name string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			out = append(out, line)
		}
	}
	return out
}

func requireSeries(t *testing.T, body, line string) {
	t.Helper()
	name, _, _ := strings.Cut(line, " ")
	name, _, _ = strings.Cut(name, "{")
	if found := series(body, name); !slices.Contains(found, line) {
		t.Errorf("%s is %q, want it to include %q", name, found, line)
	}
}

func requireNoSeries(t *testing.T, body, name string) {
	t.Helper()
	if found := series(body, name); len(found) > 0 {
		t.Errorf("%s is %q, want no series", name, found)
	}
}

func TestTheReleaseGaugeNamesTheVersionAndTheSlot(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{
		Version: machine.VersionStatus{Liken: "2026.09.10-001"},
		Boot:    machine.BootStatus{Slot: "B"},
	})
	requireSeries(t, scrape(), `liken_release_info{slot="B",version="2026.09.10-001"} 1`)
}

func TestOnlyTheRunningReleaseReports(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{
		Version: machine.VersionStatus{Liken: "2026.09.09-001"},
		Boot:    machine.BootStatus{Slot: "A"},
	})
	layer.observeStatus(&machine.MachineStatus{
		Version: machine.VersionStatus{Liken: "2026.09.10-001"},
		Boot:    machine.BootStatus{Slot: "B"},
	})

	body := scrape()
	requireSeries(t, body, `liken_release_info{slot="B",version="2026.09.10-001"} 1`)
	if lines := series(body, "liken_release_info"); len(lines) != 1 {
		t.Errorf("liken_release_info has %d series %q, want 1", len(lines), lines)
	}
}

func TestTheBootTimestampIsTheBootsOwnTime(t *testing.T) {
	booted := time.Date(2026, 9, 10, 8, 30, 0, 0, time.UTC)
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{Boot: machine.BootStatus{Time: &booted}})
	// Prometheus writes every sample value as a float.
	requireSeries(t, scrape(), fmt.Sprintf("liken_machine_boot_timestamp_seconds %g", float64(booted.Unix())))
}

func TestAMachineThatPublishedNoBootTimeReportsNone(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{})
	requireNoSeries(t, scrape(), "liken_machine_boot_timestamp_seconds")
}

func TestTheCrashTimestampIsTheCrashsOwnTime(t *testing.T) {
	crashed := time.Date(2026, 9, 2, 3, 14, 0, 0, time.UTC)
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{
		LastCrash: &machine.CrashStatus{Time: &crashed, Reason: machine.CrashPanic},
	})
	requireSeries(t, scrape(), fmt.Sprintf("liken_last_crash_timestamp_seconds %g", float64(crashed.Unix())))
}

func TestAMachineWithNoCrashOnRecordReportsNone(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{})
	requireNoSeries(t, scrape(), "liken_last_crash_timestamp_seconds")
}

func TestPendingChangesCountByTier(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{Pending: []machine.PendingDisruption{
		{Condition: "SpecConverged", Kind: machine.DisruptionReboot, Hash: "abc123abc123"},
		{Condition: "ClusterConverged", Kind: machine.DisruptionReboot, Hash: "def456def456"},
		{Condition: "CredentialsConverged", Kind: machine.DisruptionRestart, Hash: "0a10a10a10a1"},
	}})

	body := scrape()
	requireSeries(t, body, `liken_machine_change_pending{tier="reboot"} 2`)
	requireSeries(t, body, `liken_machine_change_pending{tier="restart"} 1`)
}

func TestAMachineWaitingForNothingReportsZeroPerTier(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{Pending: []machine.PendingDisruption{
		{Condition: "SpecConverged", Kind: machine.DisruptionReboot},
	}})
	layer.observeStatus(&machine.MachineStatus{})

	body := scrape()
	requireSeries(t, body, `liken_machine_change_pending{tier="reboot"} 0`)
	requireSeries(t, body, `liken_machine_change_pending{tier="restart"} 0`)
}

func TestConvergedFollowsTheSpecConvergedCondition(t *testing.T) {
	cases := []struct {
		name      string
		condition *api.Condition
		want      string
	}{
		{"true", &api.Condition{Type: "SpecConverged", Status: api.ConditionTrue, Reason: "Converged"}, "liken_machine_converged 1"},
		{"false", &api.Condition{Type: "SpecConverged", Status: api.ConditionFalse, Reason: "RebootPending"}, "liken_machine_converged 0"},
		{"unknown", &api.Condition{Type: "SpecConverged", Status: api.ConditionUnknown, Reason: "FactsIncomplete"}, ""},
		{"absent", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status := &machine.MachineStatus{}
			if c.condition != nil {
				status.Conditions = []api.Condition{*c.condition}
			}
			layer, scrape := observer(t, &fetcher{})
			layer.observeStatus(status)

			if c.want == "" {
				requireNoSeries(t, scrape(), "liken_machine_converged")
				return
			}
			requireSeries(t, scrape(), c.want)
		})
	}
}

// sliceDevice returns a ResourceSlice device with a class attribute.
func sliceDevice(name, class string) kubernetes.SliceDevice {
	device := kubernetes.SliceDevice{Name: name, Attributes: map[string]kubernetes.DeviceAttribute{}}
	if class != "" {
		device.Attributes["class"] = kubernetes.AttrString(class)
	}
	return device
}

func TestDevicesCountByClass(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeDevices([]kubernetes.SliceDevice{
		sliceDevice("pci-0000-00-02-0", "display"),
		sliceDevice("pci-0000-00-02-0-i2c-dev", "display"),
		sliceDevice("pci-0000-00-1f-3", "multimedia"),
		sliceDevice("platform-thing", ""),
	})

	body := scrape()
	requireSeries(t, body, `liken_devices{class="display"} 2`)
	requireSeries(t, body, `liken_devices{class="multimedia"} 1`)
	requireSeries(t, body, `liken_devices{class="unknown"} 1`)
}

func TestADeviceThatDisappearsReportsZero(t *testing.T) {
	layer, scrape := observer(t, &fetcher{})
	layer.observeDevices([]kubernetes.SliceDevice{sliceDevice("pci-0000-00-02-0", "display")})
	layer.observeDevices(nil)
	requireSeries(t, scrape(), `liken_devices{class="display"} 0`)
}

// downloaded returns a fetcher after one download of the release. The
// download fails when up is false.
func downloaded(t *testing.T, release *fakeRelease, up bool) *fetcher {
	t.Helper()
	var f *fetcher
	synctest.Test(t, func(t *testing.T) {
		server := serveRelease(t, new(atomic.Int64), release)
		server.SetDown(!up)
		f = fetcherFor(server)
		f.Ensure(fetchFor(release, t.TempDir(), activeSlot(t)))
		awaitSettled(f)
	})
	return f
}

func TestTheDownloadCountersReadTheFetchersTotals(t *testing.T) {
	release := makeRelease("2026.09.10-001")
	f := downloaded(t, release, true)
	_, scrape := observer(t, f)

	downloaded := 0
	for _, contents := range release.artifacts {
		downloaded += len(contents)
	}
	body := scrape()
	requireSeries(t, body, fmt.Sprintf("liken_release_download_bytes_total %d", downloaded))
	requireSeries(t, body, "liken_release_download_failures_total 0")
}

func TestADownloadThatFailsCounts(t *testing.T) {
	// The release channel is unreachable.
	f := downloaded(t, makeRelease("2026.09.10-001"), false)
	_, scrape := observer(t, f)

	body := scrape()
	requireSeries(t, body, "liken_release_download_failures_total 1")
	requireSeries(t, body, "liken_release_download_bytes_total 0")
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	f := downloaded(t, makeRelease("2026.09.10-001"), true)
	layer, scrape := observer(t, f)
	layer.observeDevices([]kubernetes.SliceDevice{sliceDevice("pci-0000-00-02-0", "display")})

	first, second := scrape(), scrape()
	for _, name := range []string{
		"liken_release_download_bytes_total",
		"liken_release_download_failures_total",
		"liken_devices",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s is %q on the first scrape and %q on the second, want the same", name, series(first, name), series(second, name))
			}
		})
	}
}
