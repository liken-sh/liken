package main

// This file is one capture from end to end: the frames out of the
// compositor, the crop, the encoder, and the body. The first frame is
// taken before the status line goes out, because the compositor's
// answer to the first capture request is where a denial arrives, and
// a denial after a 200 has no status left to carry it. Taking it
// first turns unauthorized into a 500 the caller can read.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// The buffer between the frame writer and the encoder's pipe. A crop
// is written one row at a time, and 256 KiB holds many rows of a 4K
// frame, so one row is never a write of its own.
const frameWriterBuffer = 256 << 10

// The order of a capture: the socket is opened, the first frame
// proves the compositor admits this client, the numbers are read,
// the knobs are checked against them, the encoder starts, and the
// status goes out with the encoder's first byte. A refusal after a
// 200 has nowhere to go, and an encoder whose graph the node cannot
// build writes nothing at all, so the status waits until there are
// bytes to carry.
func (s *captureServer) stream(w http.ResponseWriter, r *http.Request,
	connector, mediaType string, chosen captureSelection) *fault {
	origin := s.now()
	session, err := openCapture(s.socketPath, connector, captureOpenTimeout)
	if err != nil {
		return s.compositorFault(connector, err)
	}
	defer func() { _ = session.close() }()

	still := stillForm(mediaType)
	if still {
		session.onRetry(captureReallocates)
	} else {
		session.onRetry(captureEndsTheFrame)
	}

	first, err := session.frame()
	if err != nil {
		return s.compositorFault(connector, err)
	}
	screen := session.screen()
	s.readings.framed(mediaType)

	if f := chosen.validate(screen.Width, screen.Height, screen.Refresh); f != nil {
		return f
	}
	crop := chosen.resolve(screen.Width, screen.Height)
	// width= and height= scale the region down after the crop, so a
	// value above the region the pipe carries is the same refusal a
	// value above the screen is.
	switch {
	case chosen.Width > crop.W:
		return badRequest(fmt.Sprintf("width=%d over the region's %d", chosen.Width, crop.W))
	case chosen.Height > crop.H:
		return badRequest(fmt.Sprintf("height=%d over the region's %d", chosen.Height, crop.H))
	}

	s.report(connector, screen, mediaType)
	encoder, err := startEncoder(r.Context(), s.plan(mediaType, screen, crop, chosen))
	if err != nil {
		s.readings.failed(encoderReason)
		return newFault(http.StatusInternalServerError, problemBlank, err.Error())
	}
	defer encoder.end()

	s.readings.capturing(screenAspect, 1)
	defer s.readings.capturing(screenAspect, -1)

	// The feed runs under a context of its own, so a failure that
	// ends the request can end it too. A still that was asked to wait
	// out a t= begin is sleeping on that context, and an encoder that
	// died at once must not wait a minute for the sleeper.
	feeding, endFeed := context.WithCancel(r.Context())
	defer endFeed()
	frames := make(chan error, 1)
	go func() {
		frames <- s.feed(feeding, session, encoder, first, chosen, origin, still, mediaType)
	}()

	// Nothing of the answer is on the wire until the encoder has
	// written a byte, so an encoder that exits without writing one is
	// still a problem document the caller can read, rather than a 200
	// with an empty body that reads as a screen showing nothing.
	head, err := firstBytes(encoder.body)
	if err != nil {
		endFeed()
		_ = encoder.frames.Close()
		<-frames
		if r.Context().Err() != nil {
			return nil
		}
		s.readings.failed(encoderReason)
		return newFault(http.StatusInternalServerError, problemBlank, encoder.failure().Error())
	}

	w.Header().Set("Content-Type", contentTypeOf(mediaType))
	w.WriteHeader(http.StatusOK)
	written := writeFlushing(w, head)
	written += copyFlushing(w, encoder.body)

	feedErr := <-frames
	s.readings.wrote(screenAspect, mediaType, written)
	s.readings.streamed(screenAspect, mediaType, s.now().Sub(origin))
	if feedErr != nil {
		s.readings.failed(compositorReason)
		s.endTruncated(w, r, connector, screen, feedErr)
		return nil
	}
	if err := encoder.wait(); err != nil && r.Context().Err() == nil {
		s.readings.failed(encoderReason)
		s.endTruncated(w, r, connector, screen, err)
	}
	return nil
}

// The first bytes the encoder writes, or the error of an encoder that
// wrote none. A read that answers zero bytes and an end of file is an
// ffmpeg that exited without a packet, which is what a graph the
// node's driver cannot build looks like from here.
func firstBytes(body io.Reader) ([]byte, error) {
	buffer := make([]byte, 64<<10)
	for {
		count, err := body.Read(buffer)
		if count > 0 {
			return buffer[:count], nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// A response whose status line is already on the wire cannot become a
// problem document, so the cause goes to the log and the body ends
// without its terminating chunk. A caller then reads an unexpected
// end of file, which is the one thing that tells a truncated capture
// from a complete one. A caller that hung up first gets neither: the
// connection is already gone.
func (s *captureServer) endTruncated(w http.ResponseWriter, r *http.Request,
	connector string, screen captureScreen, err error) {
	if r.Context().Err() != nil {
		s.reportMidStream(connector, err)
		return
	}
	s.reportEnded(connector, screen, err)
	_ = http.NewResponseController(w).Flush()
	panic(http.ErrAbortHandler)
}

// The plan one request becomes: the cropped frame's own size, the
// pixel format the compositor reported, the caller's knobs, and the
// render node and graph this node encodes on. The encoder scales a
// clip wider than 1080p down unless width= or height= asks for
// another size.
func (s *captureServer) plan(mediaType string, screen captureScreen, crop cropRect, chosen captureSelection) encodePlan {
	return encodePlan{
		mediaType:   mediaType,
		width:       crop.W,
		height:      crop.H,
		pixelFormat: screen.PixelFormat,
		framerate:   chosen.Framerate,
		quality:     chosen.Quality,
		scaleWidth:  chosen.Width,
		scaleHeight: chosen.Height,
		device:      s.device,
		software:    s.usesSoftwareConversion(),
	}
}

// The graph this node converts frames with, as the info document
// names it. A reader of that document can tell a node that converts
// on the GPU from one that pays for it on the CPU, which is the
// difference between 0.08 and 0.32 cores at 1080p.
func (s *captureServer) conversionName() string {
	if s.usesSoftwareConversion() {
		return softwareGraph
	}
	return vaapiGraph
}

func (s *captureServer) usesSoftwareConversion() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.software
}

// The frames a request writes. The first one is already in hand from
// the proof. On a clip, the rest come at the rate the caller asked
// for, one capture request per tick, and every frame before the t=
// begin is taken and dropped, so frame zero of the body is origin
// plus begin.
func (s *captureServer) feed(ctx context.Context, session *captureSession, encoder *encoder,
	first []byte, chosen captureSelection, origin time.Time, still bool, mediaType string) error {
	defer func() { _ = encoder.frames.Close() }()
	writer := bufio.NewWriterSize(encoder.frames, frameWriterBuffer)
	begins := origin.Add(seconds(chosen.Time.Begin))

	if still {
		frame := first
		// A still with a t= begin waits, then takes its one frame at
		// that instant. That is the frame a capture that took and
		// dropped every frame before it would have kept, at the cost
		// of no repaints in between.
		if wait := time.Until(begins); wait > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(wait):
			}
			taken, err := session.frame()
			if err != nil {
				return err
			}
			s.readings.framed(mediaType)
			frame = taken
		}
		// The numbers are read again after the frame, because a still
		// that met a retry reallocated to a size the crop was never
		// cut for.
		stride, crop := cutOf(session, chosen)
		return writeCropped(writer, frame, stride, crop)
	}

	ends := time.Time{}
	if chosen.Time.HasEnd {
		ends = origin.Add(seconds(chosen.Time.End))
	}
	tick := time.NewTicker(time.Second / time.Duration(chosen.Framerate))
	defer tick.Stop()
	frame := first
	for {
		now := s.now()
		if !ends.IsZero() && !now.Before(ends) {
			return nil
		}
		if !now.Before(begins) {
			stride, cut := cutOf(session, chosen)
			if err := writeCropped(writer, frame, stride, cut); err != nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		taken, err := session.frame()
		if err != nil {
			var changed *captureSizeChange
			if errors.As(err, &changed) {
				return fmt.Errorf("the screen changed from %dx%d to %dx%d: %w",
					changed.OldWidth, changed.OldHeight, changed.Width, changed.Height, err)
			}
			return err
		}
		s.readings.framed(mediaType)
		frame = taken
	}
}

// The stride and the rectangle are the frame's own, read after every
// frame, because a compositor that asked for a retry answers the
// next frame at another size.
func cutOf(session *captureSession, chosen captureSelection) (int, cropRect) {
	screen := session.screen()
	return screen.Width * capturePixelBytes, chosen.resolve(screen.Width, screen.Height)
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

// The pipe carries the region and nothing else. The crop is cut in
// this write, one row at a time, so it costs one copy the encoder
// would have made anyway and no crop filter.
func writeCropped(to *bufio.Writer, frame []byte, stride int, crop cropRect) error {
	for y := crop.Y; y < crop.Y+crop.H; y++ {
		start := y*stride + crop.X*capturePixelBytes
		end := start + crop.W*capturePixelBytes
		if end > len(frame) {
			return fmt.Errorf("the frame holds %d bytes and the crop reads %d", len(frame), end)
		}
		if _, err := to.Write(frame[start:end]); err != nil {
			return err
		}
	}
	return to.Flush()
}

// The copy flushes after every block, so a still leaves the moment
// the encoder writes it and a clip arrives as it is made.
// One block, written and flushed, which is how the first bytes of a
// capture leave before the rest of the body is read.
func writeFlushing(w http.ResponseWriter, block []byte) int {
	written, _ := w.Write(block)
	_ = http.NewResponseController(w).Flush()
	return written
}

func copyFlushing(w http.ResponseWriter, body io.Reader) int {
	control := http.NewResponseController(w)
	buffer := make([]byte, 64<<10)
	written := 0
	for {
		count, err := body.Read(buffer)
		if count > 0 {
			sent, writeErr := w.Write(buffer[:count])
			written += sent
			_ = control.Flush()
			if writeErr != nil {
				return written
			}
		}
		if err != nil {
			return written
		}
	}
}
