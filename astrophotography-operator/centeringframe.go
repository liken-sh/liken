package main

// A centering frame (plan 02). The runner takes it with the same
// writes as a light frame, INDI_CLEAR, the session's and the target's
// keywords, OBJECT, and a new FRAMEID, so a centering frame never
// carries the identity or the keywords of another frame. It lands in
// the reservation's _centering directory under its FRAMEID, which no
// promotion reads and no count includes, and the worker deletes it
// after its solve.

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/outbox"
	"github.com/liken-sh/liken/astrophotography-operator/sky"
	"github.com/liken-sh/liken/astrophotography-operator/solver"
	"github.com/liken-sh/liken/astrophotography-operator/worker"
)

// solveFrame takes one centering frame with the settings given, solves
// it with a hint of the target, and deletes it.
func (w *work) solveFrame(ctx context.Context, solverName string, s sequenceSettings, target sky.Position) (solver.SolveAnswer, error) {
	frameID := newFrameID(time.Now().UTC())
	file := outbox.Frame{Camera: w.camera.name, Reservation: w.r.name, Name: frameID}
	if err := w.writeFrame(ctx, s, file, frameID); err != nil {
		return solver.SolveAnswer{}, err
	}
	scale, err := w.plateScale()
	if err != nil {
		return solver.SolveAnswer{}, err
	}
	if err := w.exposeAndWait(ctx, s, file); err != nil {
		return solver.SolveAnswer{}, fmt.Errorf("the centering frame of %s: %w", s.exposure, err)
	}
	defer w.r.o.deleteCenteringFrame(ctx, w.r.namespace, w.r.name, frameID)
	result, err := w.r.o.plateSolve(ctx, solveRequest{
		Namespace: w.r.namespace, Reservation: w.r.name, Solver: solverName,
		Frame: file.Path(), Center: target, Scale: scale,
	})
	return result.Answer, err
}

// plateScale answers the camera's plate scale in arcseconds per pixel:
// 206.265 × the pixel size in µm × the binning / the focal length in
// mm. The pixel size and the binning come from the driver, which reads
// them from the sensor and holds the binning the runner just wrote, and
// the focal length from the session's tube.
func (w *work) plateScale() (float64, error) {
	pixel := w.cameraNumber("CCD_INFO", "CCD_PIXEL_SIZE")
	if pixel <= 0 {
		return 0, fmt.Errorf("camera %s reports no pixel size in CCD_INFO, so the plate scale is unknown", w.cameraDevice)
	}
	binning := max(w.cameraNumber("CCD_BINNING", "HOR_BIN"), 1)
	session := w.res.Status.Session
	train := session.Trains[w.camera.train]
	tube, ok := session.Tubes[train.OpticalTube]
	if !ok || tube.FocalLength <= 0 {
		return 0, fmt.Errorf("the session has no OpticalTube of train %q with a focal length, so the plate scale is unknown", w.camera.train)
	}
	return 206.265 * pixel * binning / tube.FocalLength, nil
}

// cameraNumber answers a number of the camera's driver, and 0 when the
// driver does not define it.
func (w *work) cameraNumber(property, member string) float64 {
	p, _ := w.r.client.Property(w.cameraDevice, property)
	m, _ := p.Member(member)
	return m.Number
}

// deleteCenteringFrame asks the reservation's worker to delete a
// centering frame. A frame that stays costs only its space in the
// outbox, so a failure is logged and the centering goes on.
func (o *operator) deleteCenteringFrame(ctx context.Context, namespace, reservation, frameID string) {
	ctx, cancel := context.WithTimeout(ctx, workerTimeout)
	defer cancel()
	url := workerURL(namespace, reservation, worker.PathCentering+frameID).String()
	if err := o.peer(ctx, http.MethodDelete, url, nil, nil); err != nil {
		fmt.Fprintf(o.logs, "%s: deleting the centering frame %s of reservation %s/%s: %v\n", managedBy, frameID, namespace, reservation, err)
	}
}
