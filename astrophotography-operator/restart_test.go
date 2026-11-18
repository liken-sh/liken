package main

// status.exposure records the frame in flight: the frame that the
// camera exposes or the driver saves. These tests cover that frame when
// the operator stops or the reservation ends.

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func (w *runnerWorld) inFlight(t *testing.T, name string) func() bool {
	return func() bool { return w.targetNow(t, name).Status.Exposure != nil }
}

// pendingOf returns the pending count of each sequence. The runner adds
// a sequence's entry with its first frame, so it returns [0] for a
// status with no entries.
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

// The operator stops during an exposure, and the driver finishes and
// saves the frame. The next operator reads CCD_EXPOSURE before it
// writes anything. While CCD_EXPOSURE is Busy it waits. Then it counts
// the frame in status.exposure once and continues with the next frame,
// so no frame is lost or counted twice.
func TestARestartDuringAnExposureCountsTheFrameOnce(t *testing.T) {
	cases := []struct {
		name string
		// ended is true when the next operator starts after the driver
		// has finished the frame.
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
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				w.stop()
				if c.ended {
					waitUntil(t, func() bool { return len(w.worker.outboxFrames()) == 1 })
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
				if got := len(w.worker.outboxFrames()); got != 2 {
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

// When the API server refuses the status write that follows a saved
// frame, status.exposure stays set, and the next pass counts that frame
// once.
func TestARefusedStatusWriteIsRetriedOnTheNextPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
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
// the server, finishes the frame from status.exposure as it does after
// a restart, and continues. Every frame is counted once.
func TestADroppedINDIConnectionLosesNoFrame(t *testing.T) {
	cases := []struct {
		name string
		// before is how many frames were saved before the connection
		// dropped.
		before int
	}{
		{"the connection drops during the first frame", 0},
		{"the connection drops after it lasted for minutes", 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(9)))
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return len(w.worker.outboxFrames()) == c.before && w.inFlight(t, "m42")() })
				w.indi.Cut()
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
				if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{9}) {
					t.Errorf("pending %v, want [9]", got)
				}
				if got := len(w.worker.outboxFrames()); got != 9 {
					t.Errorf("%d frames in the outbox, want 9", got)
				}
				requireSample(t, scrape(t, w.o.metrics), `astrophotography_frames_exposed_total{namespace="observatory",target="m42"} 9`)
			})
		})
	}
}

// A frame in flight that cannot be counted is a path outside the
// outbox's layout, a file that is not in the outbox, or a frame of a
// camera that the INDI server does not serve. The runner clears
// status.exposure for each and counts nothing.
func TestAFrameInFlightThatIsNotInTheOutboxIsNotCounted(t *testing.T) {
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
				w.put(reservationsPath, reservationIn(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
				if got := len(w.worker.outboxFrames()); got != 1 {
					t.Errorf("%d frames, want 1", got)
				}
			})
		})
	}
}

// The worker does not respond when the next operator starts, so the
// runner cannot tell whether the frame in flight is in the outbox. It
// waits until the worker responds, then counts the frame.
func TestARestartWaitsForTheWorkerBeforeCountingTheFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, w.inFlight(t, "m42"))
		w.stop()
		waitUntil(t, func() bool { return len(w.worker.outboxFrames()) == 1 })
		w.worker.set(func() { w.worker.down = true })
		w.start(t)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if !w.inFlight(t, "m42")() {
			t.Fatal("the runner cleared the frame in flight with no response from the worker")
		}
		w.worker.set(func() { w.worker.down = false })
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{1}) {
			t.Errorf("pending %v, want [1]", got)
		}
	})
}

// After a restart, another client such as KStars is exposing the
// camera. The runner waits for the exposure to end and for the readout,
// finds no frame in the outbox, and counts nothing.
func TestARestartWaitsForAnotherClientsExposureAndCountsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(ccdINDI, "CCD_EXPOSURE")
		w.indi.SetState(ccdINDI, "CCD_EXPOSURE", "Busy")
		x := target("m42", lights(1))
		x.Status.Reservation = tonight
		x.Status.Exposure = &astrophotography.Exposure{FrameID: "0199d3a0-0000-7000-8000-000000000001",
			Reservation: tonight, File: "east-main/east-tonight/m42/M42/x.fits", StartTime: time.Now()}
		w.put(targetsPath, x)
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
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
// after a restart. The wait ends, and the release clears the frame.
func TestTheReleaseClearsAFrameInFlightWhenTheReservationEndsDuringTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.indi.Move(ccdINDI, "CCD_EXPOSURE")
		w.indi.SetState(ccdINDI, "CCD_EXPOSURE", "Busy")
		x := target("m42", lights(1))
		x.Status.Reservation = tonight
		x.Status.Exposure = &astrophotography.Exposure{FrameID: "0199d3a0-0000-7000-8000-000000000001",
			Reservation: tonight, File: "east-main/east-tonight/m42/M42/x.fits", StartTime: time.Now()}
		w.put(targetsPath, x)
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.start(t)
		time.Sleep(time.Minute)
		w.put(reservationsPath, reservationIn(observatory.ReservationDeactivating))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
		if w.inFlight(t, "m42")() {
			t.Error("the release left the frame in flight")
		}
	})
}
