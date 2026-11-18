package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// frameWriterBuffer buffers the encoder's stdin pipe, because
// writeCropped writes one row at a time and a row of a 4K frame is
// 15 KiB.
const frameWriterBuffer = 256 << 10

// stream serves one capture. It takes the first frame before it sends
// the status line, because the compositor refuses an unauthorized
// client only when it responds to the first capture request, and a refusal
// after the 200 can no longer be a problem document.
//
// stream sends the status line as soon as the encoder starts, and
// does not wait for the t= begin. The request's arrival is the zero of
// every time code, and media-api takes the moment the headers arrive
// as that zero when it combines this stream with sound. Headers sent
// late would shift the picture against the sound by the delay.
//
// A failure after the 200 ends the response with endTruncated.
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
		session.onRetry(captureEndsOnChange)
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
	// width= and height= scale the cropped region, and validate
	// checks them only against the whole screen.
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
		return newFault(http.StatusInternalServerError, problemEncoderFailed, err.Error())
	}
	defer encoder.end()

	s.readings.capturing(screenAspect, 1)
	defer s.readings.capturing(screenAspect, -1)

	plan := s.plan(mediaType, screen, crop, chosen)
	served := captureContentType(mediaType, plan.encodedWidth(), plan.encodedHeight(), plan.framerate)

	// feed waits for the t= begin on its own context, so that stream
	// can cancel the wait when the encoder exits early.
	feeding, endFeed := context.WithCancel(r.Context())
	defer endFeed()
	frames := make(chan error, 1)
	go func() {
		frames <- s.feed(feeding, session, encoder, first, chosen, origin, still, mediaType)
	}()

	written := copyFlushing(w, served, encoder.body)
	endFeed()
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
		return nil
	}
	// An encoder that exits with status 0 and writes no bytes failed
	// too, and the log gets ffmpeg's last stderr line.
	if written == 0 && r.Context().Err() == nil {
		s.readings.failed(encoderReason)
		s.endTruncated(w, r, connector, screen, encoder.failure())
	}
	return nil
}

// endTruncated ends a response whose status line is already sent. It
// logs the cause and aborts the handler, so net/http closes the
// connection without the terminating chunk. The caller reads an
// unexpected EOF and cannot mistake a damaged capture for a complete
// one. When the caller has already disconnected, it only logs.
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

// plan sizes the encoder for the crop, not the screen, because the
// pipe carries only the crop.
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

// conversionName returns the conversion graph for the info document,
// so a reader can tell whether this node converts frames on the GPU or
// the CPU.
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

// feed writes the request's frames to the encoder, starting with
// first, which stream already took. A clip takes one frame per tick of
// the framerate and drops every frame before the t= begin, so frame
// zero of the body is the frame at origin plus the begin.
func (s *captureServer) feed(ctx context.Context, session *captureSession, encoder *encoder,
	first []byte, chosen captureSelection, origin time.Time, still bool, mediaType string) error {
	defer func() { _ = encoder.frames.Close() }()
	writer := bufio.NewWriterSize(encoder.frames, frameWriterBuffer)
	begins := origin.Add(seconds(chosen.Time.Begin))

	if still {
		frame := first
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
		stride, crop := strideAndCrop(session, chosen)
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
			stride, crop := strideAndCrop(session, chosen)
			if err := writeCropped(writer, frame, stride, crop); err != nil {
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

// strideAndCrop returns the stride and crop for the frame the session
// took last. Call it after each frame, because a still's buffer can
// change size when the compositor sends a retry.
func strideAndCrop(session *captureSession, chosen captureSelection) (int, cropRect) {
	screen := session.screen()
	return screen.Width * capturePixelBytes, chosen.resolve(screen.Width, screen.Height)
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

// writeCropped writes only the crop region of the frame to the
// encoder, one row at a time, so the ffmpeg command needs no crop
// filter.
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

// copyFlushing sends the headers at once, then copies the encoder's
// output and flushes after each read, so a clip reaches the caller as
// it is encoded. It returns the number of body bytes written. nosniff
// keeps a browser from treating the body as a page.
func copyFlushing(w http.ResponseWriter, served string, body io.Reader) int {
	w.Header().Set("Content-Type", served)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	control := http.NewResponseController(w)
	_ = control.Flush()
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
