package main

// The frame in flight when the operator stops, and when the reservation
// ends.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// inFlight reports whether a target has a frame in flight.
func (w *night) inFlight(t *testing.T, name string) func() bool {
	return func() bool { return w.targetNow(t, name).Status.Exposure != nil }
}

// pendingOf answers the pending count of each sequence. The runner adds
// a sequence's entry with its first frame, so a target of one sequence
// with no entry has counted none.
func pendingOf(status astrophotography.TargetStatus) []int32 {
	out := []int32{}
	if len(status.Sequences) == 0 {
		return []int32{0}
	}
	for _, s := range status.Sequences {
		out = append(out, s.Pending)
	}
	return out
}

// The operator stops during an exposure, and the driver goes on and
// writes the frame. The next operator reads CCD_EXPOSURE before it
// writes anything: while it is Busy it waits, and then it counts the
// frame that status.exposure names once, and goes on with the next.
// No exposure is lost or repeated.
func TestARestartDuringAnExposureCountsTheFrameOnce(t *testing.T) {
	cases := []struct {
		name string
		// ended reports whether the next operator starts after the
		// driver finished the frame.
		ended bool
	}{
		{"the next operator starts while the camera exposes", false},
		{"the next operator starts after the frame is saved", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(2)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				w.stop()
				if c.ended {
					waitUntil(t, func() bool { return len(w.worker.held()) == 1 })
				}
				exposures := len(slices.DeleteFunc(w.indi.Changes(), func(change string) bool {
					return !isExposure(change)
				}))
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })

				status := w.targetNow(t, "m42").Status
				if got := pendingOf(status); !slices.Equal(got, []int32{2}) {
					t.Errorf("pending %v, want [2]", got)
				}
				if got := len(w.worker.held()); got != 2 {
					t.Errorf("%d frames in the outbox, want 2", got)
				}
				after := len(slices.DeleteFunc(w.indi.Changes(), func(change string) bool { return !isExposure(change) }))
				if exposures != 1 || after != 2 {
					t.Errorf("%d exposures before the restart and %d in all, want 1 and 2", exposures, after)
				}
				requireSample(t, scrape(t, w.o.metrics), `astrophotography_frames_exposed_total{namespace="observatory",target="m42"} 2`)
			})
		})
	}
}

func isExposure(change string) bool {
	return len(change) > len(ccdINDI) && change[:len(ccdINDI)+13] == ccdINDI+".CCD_EXPOSURE"
}

// A status write that the API server refuses after the frame is saved
// leaves status.exposure in place, and the next pass counts the frame
// from it, once.
func TestARefusedCountIsCountedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.api.mu.Lock()
		w.api.failStatus = 1
		w.api.mu.Unlock()
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{2}) {
			t.Errorf("pending %v, want [2]", got)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_frames_exposed_total{namespace="observatory",target="m42"} 2`)
	})
}

// The INDI connection drops during an exposure. The runner waits for
// the server, finishes the frame from status.exposure the way a restart
// does, and goes on. Every frame counts once.
func TestACutConnectionLosesNoFrame(t *testing.T) {
	cases := []struct {
		name string
		// before is the number of frames saved before the cut.
		before int
	}{
		{"a cut during the first frame", 0},
		{"a cut after a connection that lasted minutes", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(9)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return len(w.worker.held()) == c.before && w.inFlight(t, "m42")() })
				w.indi.Cut()
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
				if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{9}) {
					t.Errorf("pending %v, want [9]", got)
				}
				if got := len(w.worker.held()); got != 9 {
					t.Errorf("%d frames in the outbox, want 9", got)
				}
				requireSample(t, scrape(t, w.o.metrics), `astrophotography_frames_exposed_total{namespace="observatory",target="m42"} 9`)
			})
		})
	}
}

// A frame in flight that cannot be counted: a file that is no frame of
// the outbox's layout, a file the outbox does not hold, and a frame of a
// camera the server does not serve. Each clears status.exposure and
// counts nothing.
func TestAFrameInFlightThatIsNotInTheOutboxDoesNotCount(t *testing.T) {
	cases := []struct{ name, file string }{
		{"a path outside the layout", "east-main/stray.fits"},
		{"a frame the driver never wrote", "east-main/east-tonight/m42/M42/x.fits"},
		{"a frame of a camera the server does not serve", "east-guide/east-tonight/m42/M42/x.fits"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				x := target("m42", lights(1))
				x.Status.Reservation = tonight
				x.Status.Exposure = &astrophotography.Exposure{FrameID: "0199d3a0-0000-7000-8000-000000000001", Reservation: tonight, File: c.file}
				w.put(targetsPath, x)
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
				if got := len(w.worker.held()); got != 1 {
					t.Errorf("%d frames, want 1", got)
				}
			})
		})
	}
}

// The worker does not answer when the next operator starts, so the
// runner cannot tell whether the frame in flight is in the outbox. It
// waits, and counts the frame once the worker answers.
func TestARestartWaitsForTheWorkerToCountTheFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.stop()
		waitUntil(t, func() bool { return len(w.worker.held()) == 1 })
		w.worker.set(func() { w.worker.down = true })
		w.start(t)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if !w.inFlight(t, "m42")() {
			t.Fatal("the runner cleared the frame in flight with no answer from the worker")
		}
		w.worker.set(func() { w.worker.down = false })
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{1}) {
			t.Errorf("pending %v, want [1]", got)
		}
	})
}

// A camera that another client keeps exposing after a restart, such as
// KStars: the runner waits for the frame's end and the readout, finds
// no frame in the outbox, and counts nothing.
func TestARestartDoesNotWaitForAnotherClientsExposure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(ccdINDI, "CCD_EXPOSURE")
		w.indi.SetState(ccdINDI, "CCD_EXPOSURE", "Busy")
		x := target("m42", lights(1))
		x.Status.Reservation = tonight
		x.Status.Exposure = &astrophotography.Exposure{FrameID: "0199d3a0-0000-7000-8000-000000000001",
			Reservation: tonight, File: "east-main/east-tonight/m42/M42/x.fits", StartTime: time.Now()}
		w.put(targetsPath, x)
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.start(t)
		started := time.Now()
		waitUntil(t, func() bool { return !w.inFlight(t, "m42")() })
		if took := time.Since(started); took < 10*time.Second+readoutAllowance {
			t.Errorf("the runner waited %s, less than the frame and its readout", took)
		}
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{0}) {
			t.Errorf("pending %v, want [0]", got)
		}
	})
}

// The reservation ends while the runner waits for a frame in flight
// after a restart: the wait ends, and the release clears the frame.
func TestAReservationThatEndsDuringTheWaitForAFrameInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(ccdINDI, "CCD_EXPOSURE")
		w.indi.SetState(ccdINDI, "CCD_EXPOSURE", "Busy")
		x := target("m42", lights(1))
		x.Status.Reservation = tonight
		x.Status.Exposure = &astrophotography.Exposure{FrameID: "0199d3a0-0000-7000-8000-000000000001",
			Reservation: tonight, File: "east-main/east-tonight/m42/M42/x.fits", StartTime: time.Now()}
		w.put(targetsPath, x)
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.start(t)
		time.Sleep(time.Minute)
		w.put(reservationsPath, readyOrNot(observatory.ReservationDeactivating))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
		if w.inFlight(t, "m42")() {
			t.Error("the release left the frame in flight")
		}
	})
}
