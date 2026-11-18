package main

// A reservation's runner from its first target to its last frame: the
// target's status at the end, the frames in the outbox, and the Events
// of the night.

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// After the runner exposes every sequence of a target, each frame is
// pending in the outbox, the claim is released with SequencesDone, and
// no frame is in flight. Then no target is eligible, and the runner
// posts an Event on the reservation.
func TestARunnerExposesEverySequenceOfATarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2), astrophotography.Sequence{Filter: "Red", Exposure: "10s", Count: 1}))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })

		status := w.targetNow(t, "m42").Status
		var pending []int32
		for _, s := range status.Sequences {
			pending = append(pending, s.Pending)
		}
		if !slices.Equal(pending, []int32{2, 1}) {
			t.Errorf("pending %v, want [2 1]", pending)
		}
		if !slices.Equal(status.Outboxes, []astrophotography.OutboxFrames{{ClaimName: "frames", Pending: 3}}) {
			t.Errorf("outboxes %+v, want 3 frames in frames", status.Outboxes)
		}
		if status.Reservation != "" || status.Exposure != nil {
			t.Errorf("reservation %q, exposure %+v; want none", status.Reservation, status.Exposure)
		}
		frames := w.worker.outboxFrames()
		if len(frames) != 3 || !strings.HasPrefix(frames[2], "east-main/east-tonight/m42/M42/1999-12-31/Red/M42_Red_10s_") {
			t.Errorf("the outbox holds %q, want 3 frames, the third under east-main/east-tonight/m42/M42/1999-12-31/Red/", frames)
		}
		requireSample(t, scrape(t, w.o.metrics), `astrophotography_frames_exposed_total{namespace="observatory",target="m42"} 3`)
		wantTarget := []string{"Slewing", "Exposing", "SequencesDone"}
		if got := w.eventReasons("Target", "m42"); !slices.Equal(got, wantTarget) {
			t.Errorf("Events on the target %q, want %q", got, wantTarget)
		}
		if got := w.eventReasons("Reservation", tonight); !slices.Equal(got, []string{reasonIdle}) {
			t.Errorf("Events on the reservation %q, want %q", got, reasonIdle)
		}
	})
}

// The runner sends its requests to the worker of its own reservation,
// through that worker's Service.
func TestTheRunnerCallsTheReservationsWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		for _, host := range w.worker.requestHosts() {
			if host != "east-tonight-astrophotography.observatory.svc:8080" {
				t.Errorf("the runner sent a request to %s, want east-tonight-astrophotography.observatory.svc:8080", host)
			}
		}
	})
}

// The runner starts each frame as soon as the previous one is counted,
// with no wait between frames.
func TestFramesFollowEachOther(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(6)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		started := time.Now()
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		// The slew settles for 5 s, and six frames of 10 s follow.
		if took := time.Since(started); took > 70*time.Second {
			t.Errorf("six frames of 10 s took %s, want at most 70s", took)
		}
	})
}

// A reservation with no outbox images nothing, because a frame must
// land in a claim that outlives the camera's pod.
func TestAReservationWithNoOutboxImagesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		res := reservationIn(observatory.ReservationReady)
		res.Status.Session.Outbox = nil
		w.put(reservationsPath, res)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := w.eventReasons("Reservation", tonight); !slices.Equal(got, []string{reasonNoOutbox}) {
			t.Errorf("Events on the reservation %q, want %q", got, reasonNoOutbox)
		}
		if got := w.imaging(t, "m42"); got != "" {
			t.Errorf("Imaging %q, want none", got)
		}
	})
}

// A reservation with no status.endpoint has no INDI server to connect
// to, so its runner does nothing.
func TestAReservationWithNoEndpointImagesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		res := reservationIn(observatory.ReservationReady)
		res.Status.Endpoint = nil
		w.put(reservationsPath, res)
		w.start(t)
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := w.imaging(t, "m42"); got != "" {
			t.Errorf("Imaging %q, want none", got)
		}
	})
}

// refusingDialer refuses the first dials, as a server whose pod is
// still starting does, and then passes each dial to the fake server.
type refusingDialer struct {
	mu     sync.Mutex
	refuse int
	next   interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
}

func (d *refusingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	refused := d.refuse > 0
	d.refuse--
	d.mu.Unlock()
	if refused {
		return nil, &net.OpError{Op: "dial", Net: network, Err: net.UnknownNetworkError("refused")}
	}
	return d.next.DialContext(ctx, network, address)
}

// When the INDI server refuses the first dials, the runner dials again
// after a backoff that doubles, and starts imaging when the server
// accepts.
func TestARunnerReachesAServerThatStartsLate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		started := time.Now()
		w.startWith(t, &refusingDialer{refuse: 3, next: w.indi})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		// The backoff waits 1, 2, and 4 seconds.
		if took := time.Since(started); took < 7*time.Second {
			t.Errorf("the night started after %s, want at least 7s for the backoff of 1, 2, and 4 seconds", took)
		}
	})
}

// A camera that no driver serves yet has no status.indiDevice. The
// runner waits, and starts imaging when the device appears.
func TestARunnerWaitsForADeviceToBeServed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(camerasPath, &observatory.Camera{Metadata: meta("east-main")})
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := len(w.worker.outboxFrames()); got != 0 {
			t.Fatalf("the outbox holds %d frames before the camera is served, want 0", got)
		}
		w.put(camerasPath, &observatory.Camera{Metadata: meta("east-main"),
			Status: observatory.CameraStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: ccdINDI}}})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// When the API server refuses a claim, the runner tries it again after
// a wait.
func TestARefusedClaimIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.failStatusWrites(1)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// When a device's status.indiDevice names a device that the INDI server
// does not define, such as a device of another server, the runner does
// not start the night. It starts when the status names a device that
// the server defines.
func TestARunnerWaitsForADeviceTheServerDefines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(mountsPath, &observatory.Mount{Metadata: meta("east-mount"),
			Status: observatory.MountStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: "Another Mount"}}})
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if slews(w) != 0 {
			t.Fatal("the runner slewed a mount that the INDI server does not define, want no slew")
		}
		w.put(mountsPath, &observatory.Mount{Metadata: meta("east-mount"),
			Status: observatory.MountStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: mountINDI}}})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// When the store has no Observatory, the runner uses longitude 0 to
// compute the night in the frame's name, and still takes the frame.
func TestAFrameIsTakenWithNoObservatory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.api.remove(observatoriesPath, ns, "lab")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// An operator that stops while the INDI server refuses its dials stops
// waiting in the backoff at once.
func TestAnOperatorStopsWhileTheServerRefusesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(reservationsPath, reservationIn(observatory.ReservationReady))
		w.startWith(t, &refusingDialer{refuse: 1 << 30, next: w.indi})
		time.Sleep(10 * time.Second)
		stopped := time.Now()
		w.stop()
		if took := time.Since(stopped); took > time.Second {
			t.Errorf("the operator took %s to stop, want at most 1s", took)
		}
	})
}
