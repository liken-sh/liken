package main

// Layer 3 for the machine, proven the way a scraper reads it: a real
// registry, a real HTTP handler, and the text document that comes
// back. Each test gives the observer the same value the operator
// gives the API, and asserts on the series that appear.

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liken-sh/liken/api"
	"github.com/liken-sh/liken/kubernetes"
	"github.com/liken-sh/liken/machine"
)

// observer builds one operator's registry with this machine's layer 3
// on it, and returns both halves: the layer to observe with, and a
// function that scrapes what it published. The listener is off,
// because these tests read the handler directly.
func observer(t *testing.T, f *fetcher) (*machineMetrics, func() string) {
	t.Helper()
	o, layer := serveMetrics("", f)
	return layer, func() string { return scrapeHandler(t, o.Handler()) }
}

// scrapeHandler reads a registry the way Prometheus reads it: one
// HTTP GET against the real handler, and the text document that
// comes back.
func scrapeHandler(t *testing.T, handler http.Handler) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTheListenerStartsWithThisMachinesMetrics(t *testing.T) {
	o, layer := serveMetrics("127.0.0.1:0", &fetcher{})
	if o == nil || layer == nil {
		t.Fatal("the operator started with no metrics")
	}
	body := scrapeHandler(t, o.Handler())
	requireSeries(t, body, `liken_machine_change_pending{tier="reboot"} 0`)
	if len(series(body, "liken_build_info")) != 1 {
		t.Errorf("the scrape has no build info: %q", series(body, "liken_build_info"))
	}
}

// series returns every sample line in a scrape whose metric name is
// name, so a test compares numbers and never help text.
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
		t.Errorf("the scrape has no %q; it has %q", line, found)
	}
}

func requireNoSeries(t *testing.T, body, name string) {
	t.Helper()
	if found := series(body, name); len(found) > 0 {
		t.Errorf("the scrape still has %q", found)
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
		t.Errorf("a machine reported %d releases: %q", len(lines), lines)
	}
}

func TestTheBootTimestampIsTheBootsOwnTime(t *testing.T) {
	booted := time.Date(2026, 9, 10, 8, 30, 0, 0, time.UTC)
	layer, scrape := observer(t, &fetcher{})
	layer.observeStatus(&machine.MachineStatus{Boot: machine.BootStatus{Time: &booted}})
	// Prometheus renders every sample as a float, so the expected
	// line is the timestamp in that form.
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

// sliceDevice is one device as the ResourceSlice carries it, with
// the class attribute the metric counts by.
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

func TestTheDownloadCountersReadTheFetchersTotals(t *testing.T) {
	release := makeRelease("2026.09.10-001")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)

	f := &fetcher{}
	_, scrape := observer(t, f)
	f.Ensure(askFor(release, server.URL, t.TempDir(), activeSlot(t)))
	awaitSettled(t, f)

	downloaded := 0
	for _, contents := range release.artifacts {
		downloaded += len(contents)
	}
	body := scrape()
	requireSeries(t, body, fmt.Sprintf("liken_release_download_bytes_total %d", downloaded))
	requireSeries(t, body, "liken_release_download_failures_total 0")
}

func TestADownloadThatFailsCounts(t *testing.T) {
	release := makeRelease("2026.09.10-001")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)
	server.Close() // the channel is unreachable, the transient case

	f := &fetcher{}
	_, scrape := observer(t, f)
	f.Ensure(askFor(release, server.URL, t.TempDir(), activeSlot(t)))
	awaitSettled(t, f)

	body := scrape()
	requireSeries(t, body, "liken_release_download_failures_total 1")
	requireSeries(t, body, "liken_release_download_bytes_total 0")
}

func TestRepeatedScrapesLeaveTheCountersUnchanged(t *testing.T) {
	release := makeRelease("2026.09.10-001")
	var hits atomic.Int64
	server := serveRelease(t, release, &hits)

	f := &fetcher{}
	layer, scrape := observer(t, f)
	f.Ensure(askFor(release, server.URL, t.TempDir(), activeSlot(t)))
	awaitSettled(t, f)
	layer.observeDevices([]kubernetes.SliceDevice{sliceDevice("pci-0000-00-02-0", "display")})

	first, second := scrape(), scrape()
	for _, name := range []string{
		"liken_release_download_bytes_total",
		"liken_release_download_failures_total",
		"liken_devices",
	} {
		t.Run(name, func(t *testing.T) {
			if !slices.Equal(series(first, name), series(second, name)) {
				t.Errorf("%s moved between scrapes: %q then %q", name, series(first, name), series(second, name))
			}
		})
	}
}
