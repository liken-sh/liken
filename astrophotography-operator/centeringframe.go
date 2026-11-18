package main

// A centering frame is a short exposure that the runner solves to find
// where the telescope points. It gets the FITS keywords of a light
// frame and its own FRAMEID, so it never carries the identity of
// another frame. It is saved in the reservation's _centering
// directory, which promotion does not read and no frame count
// includes, and the worker deletes it after the solve.

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

// solveFrame exposes one centering frame with the given settings,
// solves it with the target's position as the hint, and deletes it.
func (w *work) solveFrame(ctx context.Context, solverName string, s sequenceSettings, target sky.Position) (solver.SolveResponse, error) {
	frameID := newFrameID(time.Now().UTC())
	file := outbox.Frame{Camera: w.camera.name, Reservation: w.r.name, Name: frameID}
	if err := w.writeFrame(ctx, s, file, frameID); err != nil {
		return solver.SolveResponse{}, err
	}
	scale, err := w.plateScale()
	if err != nil {
		return solver.SolveResponse{}, err
	}
	if err := w.exposeAndWait(ctx, s, file); err != nil {
		return solver.SolveResponse{}, fmt.Errorf("taking the centering frame of %s: %w", s.exposure, err)
	}
	defer w.r.o.deleteCenteringFrame(ctx, w.r.namespace, w.r.name, frameID)
	result, err := w.r.o.plateSolve(ctx, solveRequest{
		Namespace: w.r.namespace, Reservation: w.r.name, Solver: solverName,
		Frame: file.Path(), Center: target, Scale: scale,
	})
	return result.Response, err
}

// plateScale returns the camera's plate scale in arcseconds per pixel:
// 206.265 × pixel size in µm × binning / focal length in mm. The pixel
// size and the binning are properties of the camera's INDI driver, and
// the binning is the value the runner just wrote. The focal length is
// the session's optical tube.
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

// cameraNumber returns a number property of the camera's INDI driver,
// or 0 when the driver does not define it.
func (w *work) cameraNumber(property, member string) float64 {
	p, _ := w.r.client.Property(w.cameraDevice, property)
	m, _ := p.Member(member)
	return m.Number
}

// deleteCenteringFrame sends the reservation's worker a request to
// delete a centering frame. A frame left behind only uses space in the
// outbox, so a failure is logged and centering continues.
func (o *operator) deleteCenteringFrame(ctx context.Context, namespace, reservation, frameID string) {
	ctx, cancel := context.WithTimeout(ctx, workerTimeout)
	defer cancel()
	url := workerURL(namespace, reservation, worker.PathCentering+frameID).String()
	if err := o.peer(ctx, http.MethodDelete, url, nil, nil); err != nil {
		fmt.Fprintf(o.logs, "%s: deleting the centering frame %s of reservation %s/%s: %v\n", managedBy, frameID, namespace, reservation, err)
	}
}
