package main

// One frame from the runner to the driver: every change the runner
// sends the INDI server, in order, for a target of one frame.

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// changes answers the changes the INDI server received after the first
// n, as "<device>.<property>", with the keyword's name for a write to
// FITS_HEADER.
func changes(sent []string, from int) []string {
	var out []string
	for _, change := range sent[from:] {
		dot := strings.Index(change, ".")
		end := dot + strings.IndexByte(change[dot:]+" ", ' ')
		name, members := change[:end], strings.TrimPrefix(change[end:], " ")
		if strings.HasSuffix(name, ".FITS_HEADER") {
			keyword, _, _ := strings.Cut(strings.TrimPrefix(members, "KEYWORD_NAME="), " ")
			name += " " + keyword
		}
		out = append(out, name)
	}
	return out
}

// member answers one member's value in the last change of a property.
func member(sent []string, property, name string) string {
	for _, change := range slices.Backward(sent) {
		if !strings.HasPrefix(change, property+" ") {
			continue
		}
		for field := range strings.FieldsSeq(change) {
			if value, ok := strings.CutPrefix(field, name+"="); ok {
				return value
			}
		}
	}
	return ""
}

// The runner unparks nothing, turns tracking on, slews, and then writes
// the frame's settings in plan 01's order before the exposure: the
// upload, the frame type, the binning and gain the sequence states, the
// filter, and the keywords, INDI_CLEAR first and FRAMEID last.
func TestAFrameWritesItsSettingsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		two, hundred := int32(2), int32(100)
		w.put(targetsPath, target("m42", astrophotography.Sequence{Filter: "Luminance", Exposure: "10s", Count: 1, Binning: &two, Gain: &hundred}))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		connected := len(w.indi.Changes())
		w.playCamera(t, saves)
		w.start(t)
		time.Sleep(10 * time.Minute)
		synctest.Wait()

		sent := w.indi.Changes()
		want := []string{
			mountINDI + ".TELESCOPE_TRACK_STATE",
			mountINDI + ".EQUATORIAL_EOD_COORD",
			ccdINDI + ".UPLOAD_MODE",
			ccdINDI + ".UPLOAD_SETTINGS",
			ccdINDI + ".CCD_FRAME_TYPE",
			ccdINDI + ".CCD_BINNING",
			ccdINDI + ".CCD_GAIN",
			wheelINDI + ".FILTER_SLOT",
			ccdINDI + ".FITS_HEADER INDI_CLEAR",
			ccdINDI + ".FITS_HEADER OBSERVER",
			ccdINDI + ".FITS_HEADER TELESCOP",
			ccdINDI + ".FITS_HEADER PROGRAM",
			ccdINDI + ".FITS_HEADER OBJECT",
			ccdINDI + ".FITS_HEADER FRAMEID",
			ccdINDI + ".CCD_EXPOSURE",
		}
		if got := changes(sent, connected); !slices.Equal(got, want) {
			t.Errorf("changes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		frameID := member(sent, ccdINDI+".FITS_HEADER", "KEYWORD_VALUE")
		cases := []struct{ property, member, want string }{
			{mountINDI + ".TELESCOPE_TRACK_STATE", "TRACK_ON", "On"},
			{ccdINDI + ".UPLOAD_MODE", "UPLOAD_LOCAL", "On"},
			{ccdINDI + ".UPLOAD_SETTINGS", "UPLOAD_DIR", "/outbox/east-main/east-tonight/m42/M42/1999-12-31/Luminance"},
			{ccdINDI + ".CCD_FRAME_TYPE", "FRAME_LIGHT", "On"},
			{ccdINDI + ".CCD_BINNING", "HOR_BIN", "2"},
			{ccdINDI + ".CCD_BINNING", "VER_BIN", "2"},
			{ccdINDI + ".CCD_GAIN", "GAIN", "100"},
			{wheelINDI + ".FILTER_SLOT", "FILTER_SLOT_VALUE", "8"},
			{ccdINDI + ".CCD_EXPOSURE", "CCD_EXPOSURE_VALUE", "10"},
		}
		for _, c := range cases {
			if got := member(sent, c.property, c.member); got != c.want {
				t.Errorf("%s %s = %q, want %q", c.property, c.member, got, c.want)
			}
		}
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(frameID) {
			t.Errorf("FRAMEID %q is no UUIDv7", frameID)
		}
		prefix := member(sent, ccdINDI+".UPLOAD_SETTINGS", "UPLOAD_PREFIX")
		if !regexp.MustCompile(`^M42_Luminance_10s_20000101T\d{6}Z_` + frameID[len(frameID)-12:] + `$`).MatchString(prefix) {
			t.Errorf("UPLOAD_PREFIX %q, want the format's name and the end of FRAMEID %s", prefix, frameID)
		}
	})
}

// A frame that the driver never finishes: the runner waits for the
// exposure and the readout, does not count the frame, and sets Imaging
// False with ExposureFailed. A frame saved under another name, or not
// saved at all, does not count either. The target stays claimed, and the
// runner tries the next frame after a wait.
func TestAnExposureThatGivesNoFrameDoesNotCount(t *testing.T) {
	cases := []struct {
		name string
		play cameraPlay
		// after is how long the failure takes to show.
		after time.Duration
	}{
		{"the driver never finishes", hangs, 10*time.Second + readoutAllowance},
		{"the driver saves the frame elsewhere", savesElsewhere, 10*time.Second + savedWait},
		{"the driver neither reports nor saves the frame", loses, 10*time.Second + savedWait},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, c.play)
				w.start(t)
				waitUntil(t, w.inFlight(t, "m42"))
				started := time.Now()
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False ExposureFailed" })
				if took := time.Since(started); took < c.after || took > c.after+2*time.Second {
					t.Errorf("the failure showed after %s, want %s", took, c.after)
				}
				status := w.targetNow(t, "m42").Status
				if status.Exposure != nil || status.Reservation != tonight || !slices.Equal(pendingOf(status), []int32{0}) {
					t.Errorf("exposure %+v, reservation %q, pending %v; want none, %s, [0]", status.Exposure, status.Reservation, pendingOf(status), tonight)
				}
				requireSample(t, scrape(t, w.o.metrics), `astrophotography_exposure_failures_total{namespace="observatory",target="m42"} 1`)
				events := w.api.recorded.About("Target", "m42")
				if last := events[len(events)-1]; last.Type != "Warning" || last.Reason != astrophotography.ReasonExposureFailed {
					t.Errorf("the last Event is %s %s, want a Warning ExposureFailed", last.Type, last.Reason)
				}
			})
		})
	}
}

// The runner aborts an exposure the driver never finished, so the next
// frame's settings do not meet an exposure that still runs.
func TestAnExposureThatNeverEndsIsAborted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, hangs)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False ExposureFailed" })
		if !slices.ContainsFunc(w.indi.Changes(), func(c string) bool { return strings.HasPrefix(c, ccdINDI+".CCD_ABORT_EXPOSURE ABORT=On") }) {
			t.Errorf("no abort among %q", w.indi.Changes())
		}
	})
}

// A driver that saves the frame with no message: the worker's list of
// the outbox shows the file, and the frame counts.
func TestAFrameSavedWithNoMessageCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, savesSilently)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
		if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{1}) {
			t.Errorf("pending %v, want [1]", got)
		}
	})
}

// A sequence that states an offset writes CCD_OFFSET, and a driver that
// refuses a setting gives no frame.
func TestTheSettingsOfASequence(t *testing.T) {
	ten := int32(10)
	cases := []struct {
		name    string
		refuse  string
		imaging string
	}{
		{"an offset", "", "False SequencesDone"},
		{"a refused keyword", "FITS_HEADER", "False ExposureFailed"},
		{"a refused frame type", "CCD_FRAME_TYPE", "False ExposureFailed"},
		{"a camera whose driver is not connected", "disconnected", "False ExposureFailed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				switch c.refuse {
				case "":
				case "disconnected":
					w.disconnect(ccdINDI)
				default:
					w.indi.Refuse(ccdINDI, c.refuse)
				}
				w.put(targetsPath, target("m42", astrophotography.Sequence{Filter: "Luminance", Exposure: "10s", Count: 1, Offset: &ten}))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, saves)
				w.start(t)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == c.imaging })
				if c.refuse == "" && member(w.indi.Changes(), ccdINDI+".CCD_OFFSET", "OFFSET") != "10" {
					t.Error("no CCD_OFFSET of 10")
				}
			})
		})
	}
}

// The faults around one frame that are not the camera's: the API server
// refuses to record the frame in flight, or the worker stops answering
// before the runner can find a frame the driver saved with no message.
// Neither counts a frame, and the night goes on.
func TestAFrameTheRunnerCannotConfirmDoesNotCount(t *testing.T) {
	cases := []struct {
		name  string
		play  cameraPlay
		fault func(*night)
	}{
		{"the API server refuses the record of the frame", saves, func(w *night) {
			waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Slewing" })
			w.failStatusWrites(1)
		}},
		{"the worker stops answering during the exposure", savesSilently, func(w *night) {
			waitUntil(t, w.inFlight(t, "m42"))
			w.worker.set(func() { w.worker.down = true })
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t)
				w.put(targetsPath, target("m42", lights(1)))
				w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
				w.playCamera(t, c.play)
				w.start(t)
				c.fault(w)
				waitUntil(t, func() bool { return w.imaging(t, "m42") == "False ExposureFailed" })
				if got := pendingOf(w.targetNow(t, "m42").Status); !slices.Equal(got, []int32{0}) {
					t.Errorf("pending %v, want [0]", got)
				}
			})
		})
	}
}

// A failed exposure whose record the API server refuses too ends the
// pass, and the next pass claims the target again and goes on.
func TestAFailureThatCannotBeRecordedIsTriedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.worker.set(func() { w.worker.down = true })
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, saves)
		w.start(t)
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "True Slewing" })
		w.failStatusWrites(1)
		time.Sleep(time.Minute)
		w.worker.set(func() { w.worker.down = false })
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False SequencesDone" })
	})
}

// The reservation ends while the runner waits for the driver's message
// about the frame's file.
func TestAReservationThatEndsWhileTheRunnerChecksTheFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.put(targetsPath, target("m42", lights(1)))
		w.put(reservationsPath, readyOrNot(observatory.ReservationReady))
		w.playCamera(t, savesElsewhere)
		w.start(t)
		waitUntil(t, func() bool { return len(w.worker.held()) == 1 })
		w.put(reservationsPath, readyOrNot(observatory.ReservationDeactivating))
		waitUntil(t, func() bool { return w.imaging(t, "m42") == "False Waiting" })
	})
}
