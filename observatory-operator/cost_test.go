package main

// These tests measure the work of the operator while a reservation
// holds the east telescope and its devices report readings. The mount
// reports its coordinates four times a second as it tracks, and both
// cameras report their temperatures once a second as they cool. The
// work should grow with what changed, not with the size of the
// telescope.

import (
	"os"
	"runtime/pprof"
	"strconv"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// stream reports readings for a span of fake time, at the rates of a
// mount that tracks and two cameras that cool.
func (w *indiWorld) stream(span time.Duration) {
	const tick = 250 * time.Millisecond
	for i := range int(span / tick) {
		w.report("Telescope Simulator", "EQUATORIAL_EOD_COORD", "RA", strconv.FormatFloat(5+float64(i)/14400, 'f', 6, 64))
		if i%4 == 0 {
			celsius := strconv.FormatFloat(-float64(i)/40, 'f', 2, 64)
			w.report("CCD Simulator", "CCD_TEMPERATURE", "CCD_TEMPERATURE_VALUE", celsius)
			w.report("Guide Simulator", "CCD_TEMPERATURE", "CCD_TEMPERATURE_VALUE", celsius)
		}
		time.Sleep(tick)
	}
}

// cpuTime returns the CPU time that the process has used.
func cpuTime(t *testing.T) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatalf("reading the process CPU time: %v", err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// profiler runs the CPU profiler outside the test's bubble. The
// profiler's own goroutine never blocks durably, so a bubble that
// started it would never settle.
type profiler struct{ start, stop chan struct{} }

func startProfiler(t *testing.T, path string) *profiler {
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the CPU profile %s: %v", path, err)
	}
	p := &profiler{start: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer file.Close()
		<-p.start
		if err := pprof.StartCPUProfile(file); err != nil {
			t.Errorf("starting the CPU profile: %v", err)
		}
		p.start <- struct{}{}
		<-p.stop
		pprof.StopCPUProfile()
		p.stop <- struct{}{}
	}()
	return p
}

func (p *profiler) begin() { p.start <- struct{}{}; <-p.start }
func (p *profiler) end()   { p.stop <- struct{}{}; <-p.stop }

// TestStreamingReadingsCost measures the CPU time of a minute of
// readings, and writes its CPU profile to the file that
// OBSERVATORY_COST names. It is a measurement, not a check, so it runs
// only when the variable is set:
//
//	OBSERVATORY_COST=$PWD/cpu.out go test -run TestStreamingReadingsCost -v
func TestStreamingReadingsCost(t *testing.T) {
	path := os.Getenv("OBSERVATORY_COST")
	if path == "" {
		t.Skip("set OBSERVATORY_COST to measure the cost of streaming readings")
	}
	profile := startProfiler(t, path)
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		before := cpuTime(t)
		profile.begin()
		w.indi.stream(time.Minute)
		synctest.Wait()
		profile.end()
		t.Logf("a minute of readings took %v of CPU time", cpuTime(t)-before)
	})
}

// A reading wakes only the status writer, which then waits for its
// window. The supervisor and the runner of a Ready telescope read only
// the stores and the devices that each server defines, and a reading
// changes neither, so they must not run. The test checks this through
// the allocations of one reading.
//
// AllocsPerRun counts the whole process, so the test does not run in
// parallel with others.
func TestAReadingWakesOnlyTheStatusWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := readyWorld(t)
		ra := 5.0
		allocs := testing.AllocsPerRun(20, func() {
			ra += 0.001
			w.indi.report("Telescope Simulator", "EQUATORIAL_EOD_COORD", "RA", strconv.FormatFloat(ra, 'f', 6, 64))
			synctest.Wait()
		})
		if allocs > 250 {
			t.Errorf("%v allocations for one reading, want at most 250", allocs)
		}
	})
}

// While readings arrive and statuses change, the runner of a Ready
// telescope sends no create for a pod, a Service, or the
// ResourceClaimTemplate of a device with spec.claim.
func TestReadingsCreateNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.claimFocuser()
		w.reserve("east-tonight", map[string]any{"telescope": "east", "holder": "desktop"})
		w.phase("east-tonight", observatory.ReservationReady, 10*time.Minute)
		time.Sleep(2 * statusWindow)
		synctest.Wait()
		creates := func() int {
			return w.api.creates(templatesCollection) + w.api.creates(podsCollection) + w.api.creates(servicesCollection)
		}
		before := creates()
		w.indi.stream(10 * time.Second)
		synctest.Wait()
		if after := creates(); after != before {
			t.Errorf("%d creates during 10 s of readings, want none", after-before)
		}
	})
}
