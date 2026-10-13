package main

// The world of a runner's test: the fake API server with an observatory
// whose telescope east has a mount, an imaging camera, and a filter
// wheel on the train east-imaging; a fake INDI server that runs the
// telescope, CCD, and filter wheel simulators from their transcripts;
// and a fake worker that lists the outbox. Each test declares its
// targets and its reservation, starts the operator, and plays the
// camera's exposures.

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/observatory-operator/indi"
	"github.com/liken-sh/liken/observatory-operator/indi/inditest"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	ns        = "observatory"
	tonight   = "east-tonight"
	ccdINDI   = "CCD Simulator"
	wheelINDI = "Filter Simulator"
	mountINDI = "Telescope Simulator"

	observatoriesPath = "/apis/observatory.liken.sh/v1alpha1/observatories"
	mountsPath        = "/apis/observatory.liken.sh/v1alpha1/mounts"
	filterWheelsPath  = "/apis/observatory.liken.sh/v1alpha1/filterwheels"
)

type night struct {
	// t is the test of the bubble the world lives in.
	t      *testing.T
	api    *fakeAPI
	indi   *inditest.Server
	worker *fakeWorker
	o      *operator
	// stop stops the operator, as a SIGTERM does, and waits for it.
	stop func()
	// configure changes the operator before it runs.
	configure func(*operator)
}

// newWorld declares the observatory and connects the simulators, as
// observatory-operator's Connect step does before a reservation is
// Ready.
func newWorld(t *testing.T) *night {
	w := &night{t: t, api: startFakeAPI(t), indi: inditest.NewServer(t, "telescope", "ccd", "wheel")}
	w.worker = startFakeWorker(t)
	w.put(observatoriesPath, &observatory.Observatory{Metadata: meta("lab"),
		Spec: observatory.ObservatorySpec{Location: observatory.Location{Latitude: 51.5, Longitude: -75}}})
	w.put(mountsPath, &observatory.Mount{Metadata: meta("east-mount"),
		Status: observatory.MountStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: mountINDI}}})
	w.put(camerasPath, &observatory.Camera{Metadata: meta("east-main"),
		Status: observatory.CameraStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: ccdINDI}}})
	w.put(filterWheelsPath, &observatory.FilterWheel{Metadata: meta("east-wheel"),
		Status: observatory.FilterWheelStatus{DeviceStatus: observatory.DeviceStatus{IndiDevice: wheelINDI}}})
	for _, claim := range []string{"frames", "archive"} {
		w.api.put(claimsPath, object(ns, claim, nil, map[string]any{}))
	}
	connectSimulators(t, w.indi)
	return w
}

func meta(name string) observatory.ObjectMeta {
	return observatory.ObjectMeta{Name: name, Namespace: ns, UID: ns + "/" + name, Generation: 1}
}

// put stores a typed object in a collection.
func (w *night) put(path string, object any) {
	body, _ := json.Marshal(object)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	w.api.put(path, out)
}

func connectSimulators(t *testing.T, server *inditest.Server) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := indi.NewClient("indiserver", indi.WithDialer(server))
	go func() { _ = client.Run(ctx) }()
	for _, device := range []string{mountINDI, ccdINDI, wheelINDI} {
		if err := client.ConnectDevice(ctx, device); err != nil {
			t.Fatal(err)
		}
	}
}

// reservation is east-tonight, Ready, with the session that
// observatory-operator records: the three devices, the outbox frames,
// and the session's keywords for the camera.
func readyOrNot(phase observatory.ReservationPhase) *observatory.Reservation {
	return &observatory.Reservation{
		Metadata: meta(tonight),
		Spec:     observatory.ReservationSpec{Telescope: "east", Holder: managedBy, Outbox: &observatory.Outbox{ClaimName: "frames"}},
		Status: observatory.ReservationStatus{
			Phase:    phase,
			Endpoint: &observatory.Endpoint{Service: "east-indi", Host: "east-indi.observatory.svc", Port: 7624},
			Session: &observatory.Session{
				Observatory: "lab",
				Devices: map[string]observatory.SessionDevice{
					"Camera/east-main":       {OpticalTrain: "east-imaging"},
					"FilterWheel/east-wheel": {OpticalTrain: "east-imaging"},
					"Mount/east-mount":       {Telescope: "east"},
				},
				Outbox: &observatory.Outbox{ClaimName: "frames"},
				FITSKeywords: map[string][]observatory.FITSKeyword{
					"Camera/east-main": {{Name: "OBSERVER", Value: "Ada"}, {Name: "TELESCOP", Value: "east"}},
				},
			},
		},
	}
}

// target is a target of M42 on east-imaging, with one sequence.
func target(name string, sequences ...astrophotography.Sequence) *astrophotography.Target {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := meta(name)
	m.CreationTimestamp = &created
	return &astrophotography.Target{
		Metadata: m,
		Spec: astrophotography.TargetSpec{
			Object:       "M42",
			Coordinates:  astrophotography.Coordinates{RA: "05:35:17.3", Dec: "-05:23:28"},
			OpticalTrain: "east-imaging",
			Sequences:    sequences,
			Archive:      astrophotography.Archive{ClaimName: "archive", Format: "{object}/{night}/{filter}/{object}_{filter}_{exposure}_{datetime}", Promotion: astrophotography.PromotionContinuous},
			FITSKeywords: []observatory.FITSKeyword{{Name: "PROGRAM", Value: "Orion"}},
		},
	}
}

func lights(count int32) astrophotography.Sequence {
	return astrophotography.Sequence{Filter: "Luminance", Exposure: "10s", Count: count}
}

// start runs the operator until the test ends, or until stop.
func (w *night) start(t *testing.T) { w.startWith(t, w.indi) }

// startWith runs the operator with a dialer of the INDI server.
func (w *night) startWith(t *testing.T, dialer indi.Dialer) {
	ctx, cancel := context.WithCancel(t.Context())
	client := apiclient.New(apiservertest.Host, w.api.server.Client(), "")
	recorder := events.New(t.Context(), client, managedBy, events.Options{Log: io.Discard})
	o := newOperator(client, recorder, newMetrics("dev"), testLog{t})
	o.indiDialer = dialer
	o.workerClient = w.worker.server.Client()
	if w.configure != nil {
		w.configure(o)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.run(ctx, w.api.watcher(t))
	}()
	w.o = o
	w.stop = func() {
		cancel()
		<-done
	}
	t.Cleanup(w.stop)
}

// waitUntil lets the bubble's clock run, a second at a time, until
// done holds, and fails the test after an hour.
func waitUntil(t *testing.T, done func() bool) {
	t.Helper()
	for range 3600 {
		synctest.Wait()
		if done() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("waited an hour")
}

// testLog writes the operator's log lines to the test's log.
type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(string(p))
	return len(p), nil
}

// targetNow answers a target as the fake API server holds it.
func (w *night) targetNow(t *testing.T, name string) *astrophotography.Target {
	t.Helper()
	object := w.api.get(targetsPath, ns, name)
	if object == nil {
		t.Fatalf("no target %s", name)
	}
	body, _ := json.Marshal(object)
	var out astrophotography.Target
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

// imaging answers a target's Imaging condition as "<status> <reason>".
func (w *night) imaging(t *testing.T, name string) string {
	for _, c := range w.targetNow(t, name).Status.Conditions {
		if c.Type == astrophotography.ConditionImaging {
			return string(c.Status) + " " + c.Reason
		}
	}
	return ""
}

// eventReasons answers the reasons of the Events about one object, in
// order.
func (w *night) eventReasons(kind, name string) []string {
	var out []string
	for _, e := range w.api.recorded.About(kind, name) {
		out = append(out, e.Reason)
	}
	return out
}

// failStatusWrites makes the fake API server refuse the next status
// writes.
func (w *night) failStatusWrites(count int) {
	w.api.mu.Lock()
	defer w.api.mu.Unlock()
	w.api.failStatus = count
}

// disconnect disconnects a simulator's driver from its hardware, so it
// deletes the properties it defined on connect.
func (w *night) disconnect(device string) {
	t := w.t
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := indi.NewClient("indiserver", indi.WithDialer(w.indi))
	go func() { _ = client.Run(ctx) }()
	if err := client.DisconnectDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
}

// imagingIn answers the message of the Imaging condition in the body of
// a status write.
func imagingIn(body map[string]any) string {
	status, _ := body["status"].(map[string]any)
	list, _ := status["conditions"].([]any)
	for _, c := range list {
		if condition, _ := c.(map[string]any); condition["type"] == astrophotography.ConditionImaging {
			message, _ := condition["message"].(string)
			return message
		}
	}
	return ""
}
