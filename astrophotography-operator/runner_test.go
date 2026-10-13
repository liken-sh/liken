package main

// A reservation's runner from its first target to its last frame:
// what the target's status holds at the end, the frames in the outbox,
// and the Events of the night.

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

// A target whose sequences the runner exposes in full ends with every
// frame pending in the outbox, its claim released with SequencesDone,
// and no frame in flight. Then no target is eligible, and the
// reservation says so.
func TestARunnerExposesEverySequenceOfATarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(2), astrophotography.Sequence{Filter: "Red", Exposure: "10s", Count: 1}))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
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
		frames := w.worker.held()
		if len(frames) != 3 || !strings.HasPrefix(frames[2], "east-main/east-tonight/m42/M42/1999-12-31/Red/M42_Red_10s_") {
			t.Errorf("frames in the outbox %q", frames)
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

// The worker that the runner asks is the reservation's own, by its
// Service.
func TestTheRunnerAsksTheReservationsWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		for _, host := range w.worker.asked() {
			if host != "east-tonight-astrophotography.observatory.svc:8080" {
				t.Errorf("asked %s", host)
			}
		}
	})
}

// Each runner waits between frames for nothing but the frame: a night
// of short frames spends its time exposing.
func TestFramesFollowEachOther(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(6)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		started := time.Now()
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		// The slew settles for 5 s, and six frames of 10 s follow.
		if took := time.Since(started); took > 70*time.Second {
			t.Errorf("six frames of 10 s took %s", took)
		}
	})
}

// A reservation with no outbox images nothing, because a frame must
// land in a claim that outlives the camera's pod.
func TestAReservationWithNoOutboxImagesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		res := readyOrNot(observatory.ReservationReady)
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

// A reservation with no endpoint names no server, and its runner does
// nothing.
func TestAReservationWithNoEndpointImagesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		res := readyOrNot(observatory.ReservationReady)
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

// refusingDialer refuses the first dials, the way a server whose pod is
// starting does, and then hands each dial to the fake server.
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

// A server the runner cannot reach yet is dialed again after a backoff
// that grows, and the night starts when it answers.
func TestARunnerReachesAServerThatStartsLate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		started := time.Now()
		w.startWith(t, &refusingDialer{refuse: 3, next: w.indi})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		// The backoff waits 1, 2, and 4 seconds.
		if took := time.Since(started); took < 7*time.Second {
			t.Errorf("the night started after %s, before the backoff of 7 s", took)
		}
	})
}

// A camera whose driver does not serve it yet has no status.indiDevice.
// The runner waits, and images when the device appears.
func TestARunnerWaitsForADeviceToBeServed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(camerasPath, &observatory.Camera{Metadata: meta("east-main")})
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := len(w.worker.held()); got != 0 {
			t.Fatalf("%d frames before the camera is served", got)
		}
		w.put(camerasPath, &observatory.Camera{Metadata: meta("east-main"),
			Status: observatory.CameraStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: ccdINDI}}})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// A claim that the API server refuses is tried again after a wait.
func TestARefusedClaimIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.failStatusWrites(1)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// A device that the server does not define, such as a mount whose
// status names a device of another server, holds the night until its
// status names a device the server has.
func TestARunnerWaitsForADeviceTheServerDefines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(mountsPath, &observatory.Mount{Metadata: meta("east-mount"),
			Status: observatory.MountStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: "Another Mount"}}})
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if slews(w) != 0 {
			t.Fatal("the runner slewed a mount the server does not define")
		}
		w.put(mountsPath, &observatory.Mount{Metadata: meta("east-mount"),
			Status: observatory.MountStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: mountINDI}}})
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// An observatory the store does not hold gives the frame's night at
// Greenwich, and the frame is still named and taken.
func TestAFrameIsTakenWithNoObservatory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.api.remove(observatoriesPath, ns, "lab")
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// An operator that stops while the INDI server refuses its dials
// returns from its backoff at once.
func TestAnOperatorStopsWhileTheServerRefusesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.startWith(t, &refusingDialer{refuse: 1 << 30, next: w.indi})
		time.Sleep(10 * time.Second)
		stopped := time.Now()
		w.stop()
		if took := time.Since(stopped); took > time.Second {
			t.Errorf("the operator took %s to stop", took)
		}
	})
}
