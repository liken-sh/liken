package main

// The operator's cost while a reservation holds the east telescope and
// its devices report readings: the mount reports its coordinates four
// times a second as it tracks, and both cameras report their
// temperatures once a second as they cool. The work should follow what
// changed, not the size of the telescope.

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

// cpuTime answers the CPU time the process has used.
func cpuTime(t *testing.T) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	p := &profiler{start: make(chan struct{}), stop: make(chan struct{})}
	go func() {
		defer file.Close()
		<-p.start
		if err := pprof.StartCPUProfile(file); err != nil {
			t.Error(err)
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

// A reading wakes the status writer, which waits for its window, and
// nothing else. The supervisor and the runner of a Ready telescope read
// only the stores and the devices that each server defines, and a
// reading changes neither. When every goroutine read the stores again
// and the runner built each pod spec again, a reading cost about 1,460
// allocations.
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

// A device with spec.claim gets its ResourceClaim once. The claim
// cannot change, and the runner of a Ready telescope does not create it
// again while readings arrive and statuses change.
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
			return w.api.creates(claimsCollection) + w.api.creates(podsCollection) + w.api.creates(servicesCollection)
		}
		before := creates()
		w.indi.stream(10 * time.Second)
		synctest.Wait()
		if after := creates(); after != before {
			t.Errorf("%d creates during 10 s of readings, want none", after-before)
		}
	})
}
