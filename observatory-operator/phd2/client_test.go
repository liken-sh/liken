package phd2

import (
	"context"
	"errors"
	"math"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
	"time"
)

// running starts a client of the fake and runs it until the test ends.
func running(t *testing.T, f *phd2test.Server) *Client {
	c := NewClient("east-guider.observatory.svc:4400", WithDialer(f))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(t.Context())
	}()
	t.Cleanup(func() { <-done })
	return c
}

// settled waits until every goroutine in the bubble is blocked, and
// answers the client's state then.
func settled(c *Client) State {
	synctest.Wait()
	return c.State()
}

func TestTheBaselineReadsTheStateAfterTheStreamOpens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) {
			f.AppState, f.Calibrated, f.Equipment, f.Scale = "Looping", true, true, phd2test.Pointer(2.48)
		})
		c := running(t, f)
		s := settled(c)
		if !s.Open || s.Version != "2.6.14" || s.AppState != "Looping" {
			t.Errorf("state = %+v", s)
		}
		if s.Calibrated == nil || !*s.Calibrated || s.Equipment == nil || !*s.Equipment {
			t.Errorf("calibrated = %v, equipment = %v", s.Calibrated, s.Equipment)
		}
		if s.PixelScale == nil || *s.PixelScale != 2.48 {
			t.Errorf("pixel scale = %v", s.PixelScale)
		}
		if got, want := f.Received(), "get_app_state get_calibrated get_connected get_pixel_scale"; got != want {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
}

func TestAPixelScaleOfNullIsUnknown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := running(t, phd2test.New())
		if s := settled(c); s.PixelScale != nil {
			t.Errorf("pixel scale = %v, want unknown", *s.PixelScale)
		}
	})
}

func TestEventsMoveTheAppState(t *testing.T) {
	cases := []struct {
		event string
		want  string
	}{
		{"LoopingExposures", "Looping"},
		{"StartCalibration", "Calibrating"},
		{"Calibrating", "Calibrating"},
		{"StartGuiding", "Guiding"},
		{"GuideStep", "Guiding"},
		{"StarLost", "LostLock"},
		{"Paused", "Paused"},
		{"AppState", "Selected"},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := phd2test.New()
				c := running(t, f)
				settled(c)
				f.Broadcast(phd2test.Event(tc.event, map[string]any{"State": "Selected"}))
				if s := settled(c); s.AppState != tc.want {
					t.Errorf("app state = %q, want %q", s.AppState, tc.want)
				}
			})
		})
	}
}

// PHD2 sends AppState only to a new connection, and no event says
// where a stop leaves it: a guider that stops guiding can still loop.
// So the client reads the state again after each stop.
func TestAStopReadsTheAppStateAgain(t *testing.T) {
	for _, stop := range []string{"GuidingStopped", "LoopingExposuresStopped"} {
		t.Run(stop, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := phd2test.New()
				c := running(t, f)
				settled(c)
				f.Broadcast(phd2test.Event("StartGuiding", nil))
				settled(c)
				f.Set(func(f *phd2test.Server) { f.AppState = "Looping" })
				f.Clear()
				f.Broadcast(phd2test.Event(stop, nil))
				if s := settled(c); s.AppState != "Looping" {
					t.Errorf("app state = %q, want Looping", s.AppState)
				}
				if got := f.Received(); got != "get_app_state" {
					t.Errorf("requests = %q", got)
				}
			})
		})
	}
}

func TestCalibrationEventsMoveCalibrated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.Calibrated = true })
		c := running(t, f)
		settled(c)
		f.Broadcast(phd2test.Event("StartCalibration", map[string]any{"Mount": "INDI Mount [Telescope Simulator]"}))
		if s := settled(c); s.Calibrated == nil || *s.Calibrated {
			t.Errorf("calibrated during calibration = %v", s.Calibrated)
		}
		f.Broadcast(phd2test.Event("CalibrationComplete", map[string]any{"Mount": "INDI Mount [Telescope Simulator]"}))
		if s := settled(c); s.Calibrated == nil || !*s.Calibrated {
			t.Errorf("calibrated after calibration = %v", s.Calibrated)
		}
	})
}

func TestAFailedCalibrationIsAnAlert(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		c := running(t, f)
		settled(c)
		f.Broadcast(phd2test.Event("CalibrationFailed", map[string]any{"Mount": "INDI Mount [Telescope Simulator]", "Reason": "star did not move enough"}))
		s := settled(c)
		if s.Alert == nil || s.Alert.Type != "error" || s.Alert.Message != "Calibration failed: star did not move enough" {
			t.Errorf("alert = %+v", s.Alert)
		}
	})
}

func TestGuideStepsGiveTheRMSOfTheirDistances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		c := running(t, f)
		settled(c)
		f.Broadcast(phd2test.Event("StartGuiding", nil))
		f.Broadcast(phd2test.GuideStep(1, 3, 0))
		f.Broadcast(phd2test.GuideStep(2, -1, 4))
		s := settled(c)
		if s.RMS.Steps != 2 || s.RMS.RA != math.Sqrt(5) || s.RMS.Dec != math.Sqrt(8) || s.RMS.Total != math.Sqrt(13) {
			t.Errorf("rms = %+v", s.RMS)
		}
		if s.Step == nil || s.Step.Frame != 2 || s.Step.SNR != 41.25 || s.Step.HFD != 2.31 {
			t.Errorf("step = %+v", s.Step)
		}
		if want := time.Unix(1790000000, 500_000_000).UTC(); !s.Step.Time.Equal(want) {
			t.Errorf("step time = %v, want %v", s.Step.Time, want)
		}
		f.Broadcast(phd2test.Event("StartGuiding", nil))
		if s := settled(c); s.RMS.Steps != 0 {
			t.Errorf("rms after a new start = %+v", s.RMS)
		}
	})
}

func TestTheRMSCoversTheLastWindowOfSteps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		c := running(t, f)
		settled(c)
		f.Broadcast(phd2test.GuideStep(1, 100, 100))
		for frame := 2; frame <= RMSWindow+1; frame++ {
			f.Broadcast(phd2test.GuideStep(frame, 1, 1))
		}
		if s := settled(c); s.RMS.Steps != RMSWindow || s.RMS.RA != 1 || s.RMS.Dec != 1 {
			t.Errorf("rms = %+v", s.RMS)
		}
	})
}

func TestAnAlertIsKeptAndReadsTheEquipmentAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.Equipment = true })
		c := running(t, f)
		settled(c)
		f.Set(func(f *phd2test.Server) { f.Equipment = false })
		f.Clear()
		f.Broadcast(phd2test.Event("Alert", map[string]any{"Msg": "INDI camera disconnected", "Type": "error"}))
		s := settled(c)
		if s.Alert == nil || s.Alert.Message != "INDI camera disconnected" || s.Alert.Type != "error" {
			t.Errorf("alert = %+v", s.Alert)
		}
		if s.Equipment == nil || *s.Equipment {
			t.Errorf("equipment = %v, want false", s.Equipment)
		}
		if got := f.Received(); got != "get_calibrated get_connected get_pixel_scale" {
			t.Errorf("requests = %q", got)
		}
	})
}

func TestSetConnectedConnectsTheEquipment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		c := running(t, f)
		if err := c.WaitFor(t.Context(), func(s State) bool { return s.Open }); err != nil {
			t.Fatal(err)
		}
		if err := c.SetConnected(t.Context(), true); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); s.Equipment == nil || !*s.Equipment {
			t.Errorf("equipment = %v", s.Equipment)
		}
	})
}

func TestARefusedConnectAnswersPHD2sMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.Refuse = "equipment failed to connect: camera" })
		c := running(t, f)
		settled(c)
		err := c.SetConnected(t.Context(), true)
		if err == nil || err.Error() != "phd2: set_connected: equipment failed to connect: camera" {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStopCaptureStopsPHD2(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.AppState = "Guiding" })
		c := running(t, f)
		settled(c)
		if err := c.StopCapture(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); s.AppState != "Stopped" {
			t.Errorf("app state = %q", s.AppState)
		}
	})
}

func TestACallWithNoConnectionFails(t *testing.T) {
	c := NewClient("east-guider.observatory.svc:4400")
	if err := c.StopCapture(t.Context()); !errors.Is(err, ErrNotConnected) {
		t.Errorf("err = %v", err)
	}
}

func TestTheStateEmptiesWhenTheConnectionEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.AppState, f.Equipment = "Guiding", true })
		c := NewClient("east-guider.observatory.svc:4400", WithDialer(f))
		ended := make(chan error, 1)
		go func() { ended <- c.Run(t.Context()) }()
		settled(c)
		f.Cut()
		if err := <-ended; !errors.Is(err, ErrDisconnected) {
			t.Errorf("run ended with %v", err)
		}
		if s := c.State(); s.Open || s.AppState != "" || s.Equipment != nil {
			t.Errorf("state after the end = %+v", s)
		}
	})
}

func TestACallInFlightFailsWhenTheConnectionEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		f.Set(func(f *phd2test.Server) { f.Hold = "stop_capture" })
		c := running(t, f)
		settled(c)
		failed := make(chan error, 1)
		go func() { failed <- c.StopCapture(t.Context()) }()
		synctest.Wait()
		f.Cut()
		if err := <-failed; !errors.Is(err, ErrDisconnected) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestASecondRunIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		c := running(t, f)
		settled(c)
		if err := c.Run(t.Context()); !errors.Is(err, ErrRunning) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestEachChangeCallsNotify(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := phd2test.New()
		rings := make(chan struct{}, 100)
		c := NewClient("east-guider.observatory.svc:4400", WithDialer(f), WithNotify(func(State) { rings <- struct{}{} }))
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = c.Run(ctx)
		}()
		settled(c)
		before := len(rings)
		f.Broadcast(phd2test.GuideStep(1, 1, 1))
		settled(c)
		if len(rings) != before+1 {
			t.Errorf("notify ran %d times for one step", len(rings)-before)
		}
		cancel()
		<-done
	})
}
