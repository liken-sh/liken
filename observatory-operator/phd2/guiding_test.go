package phd2

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// guiding returns a client of a fake PHD2 whose equipment is connected,
// after the client has read its baseline.
func guiding(t *testing.T, change func(*phd2test.Server)) (*Client, *phd2test.Server) {
	f := phd2test.New()
	f.Set(func(f *phd2test.Server) { f.Equipment = true })
	f.Set(change)
	c := running(t, f)
	settled(c)
	return c, f
}

var aSettle = Settle{Pixels: 1.5, Time: 5 * time.Second, Timeout: time.Minute}

func TestSetExposureSendsMilliseconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(*phd2test.Server) {})
		if err := c.SetExposure(t.Context(), 1500*time.Millisecond); err != nil {
			t.Fatalf("setting the exposure: %v", err)
		}
		if f.Exposure != 1500 {
			t.Errorf("PHD2 exposure is %d ms, want 1500", f.Exposure)
		}
	})
}

func TestLoopFindStarAndGuideSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(*phd2test.Server) {})
		if err := c.Loop(t.Context()); err != nil {
			t.Fatalf("starting the loop: %v", err)
		}
		if s := settled(c); s.AppState != "Looping" {
			t.Errorf("state after Loop is %q, want Looping", s.AppState)
		}
		x, y, err := c.FindStar(t.Context())
		if err != nil || x != 1045.09 || y != 44.96 {
			t.Errorf("FindStar returned (%v, %v, %v), want (1045.09, 44.96, nil)", x, y, err)
		}
		if err := c.Guide(t.Context(), aSettle, false); err != nil {
			t.Fatalf("starting the guide: %v", err)
		}
		if err := c.WaitSettled(t.Context()); err != nil {
			t.Errorf("WaitSettled after Guide returned %v, want nil", err)
		}
		s := settled(c)
		if s.AppState != "Guiding" || s.Calibrated == nil || !*s.Calibrated {
			t.Errorf("state after Guide is %+v, want Guiding and calibrated", s)
		}
		want := Settling{Began: true, Distance: 0.67, SettleTime: 5 * time.Second, StarLocked: true, Done: true, TotalFrames: 7}
		if *s.Settling != want {
			t.Errorf("settling is %+v, want %+v", *s.Settling, want)
		}
	})
}

// PHD2 reads a settle's times as whole seconds, so the request rounds
// each one up.
func TestASettleSendsWholeSecondsRoundedUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(f *phd2test.Server) { f.AppState = "Guiding" })
		settle := Settle{Pixels: 0.8, Time: 4200 * time.Millisecond, Timeout: 30 * time.Second}
		if err := c.Dither(t.Context(), 3, true, settle); err != nil {
			t.Fatalf("starting the dither: %v", err)
		}
		want := `{"amount":3,"raOnly":true,"settle":{"pixels":0.8,"time":5,"timeout":30}}`
		if got := string(f.Params("dither")); got != want {
			t.Errorf("dither params are %s, want %s", got, want)
		}
	})
}

func TestAFailedSettleReturnsPHD2sReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(f *phd2test.Server) {
			f.AppState, f.SettleFailure = "Guiding", "timed-out waiting for guider to settle"
		})
		if err := c.Dither(t.Context(), 5, false, aSettle); err != nil {
			t.Fatalf("starting the dither: %v", err)
		}
		err := c.WaitSettled(t.Context())
		if !errors.Is(err, ErrSettleFailed) || err.Error() != "phd2: the guide or dither did not settle: timed-out waiting for guider to settle" {
			t.Errorf("WaitSettled returned %v, want ErrSettleFailed with PHD2's reason", err)
		}
		if s := c.State(); s.Settling.Status != 1 {
			t.Errorf("settling status is %d, want 1", s.Settling.Status)
		}
	})
}

// A refused dither starts no settle, so a wait after it does not wait
// for a SettleDone that never comes.
func TestARefusedDitherStartsNoSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(*phd2test.Server) {})
		err := c.Dither(t.Context(), 5, false, aSettle)
		if err == nil || err.Error() != "phd2: dither: Dither error" {
			t.Errorf("Dither returned %v, want PHD2's refusal", err)
		}
		if err := c.WaitSettled(t.Context()); !errors.Is(err, ErrNotSettling) {
			t.Errorf("WaitSettled returned %v, want ErrNotSettling", err)
		}
	})
}

// The settle reports its progress while it runs, and a wait ends when
// the connection does.
func TestASettleInProgressAndAConnectionThatEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(f *phd2test.Server) { f.AppState, f.HoldSettle = "Guiding", true })
		if err := c.Dither(t.Context(), 5, false, aSettle); err != nil {
			t.Fatalf("starting the dither: %v", err)
		}
		if s := settled(c); !s.Settling.Began || s.Settling.Done || s.Settling.Distance != 0.67 {
			t.Errorf("settling is %+v, want begun, not done, at distance 0.67", s.Settling)
		}
		ended := make(chan error, 1)
		go func() { ended <- c.WaitSettled(t.Context()) }()
		synctest.Wait()
		f.Cut()
		if err := <-ended; !errors.Is(err, ErrDisconnected) {
			t.Errorf("WaitSettled after the connection ended returned %v, want ErrDisconnected", err)
		}
	})
}

// A settle that another client started, such as a dither from KStars,
// is recorded from its first event, and a SettleBegin while a settle
// is open starts a new record.
func TestASettleThatAnotherClientStarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(*phd2test.Server) {})
		f.Broadcast(phd2test.Event("Settling", map[string]any{"Distance": 2.5, "Time": 1.0, "SettleTime": 10.0, "StarLocked": true}))
		if s := settled(c); s.Settling == nil || s.Settling.Distance != 2.5 || s.Settling.Time != time.Second {
			t.Errorf("settling is %+v, want distance 2.5 and time 1s from the Settling event", s.Settling)
		}
		f.Broadcast(phd2test.Event("SettleBegin", nil))
		if s := settled(c); s.Settling.Distance != 0 || !s.Settling.Began {
			t.Errorf("settling after a second SettleBegin is %+v, want a new record at distance 0", s.Settling)
		}
		f.Broadcast(phd2test.Event("SettleDone", map[string]any{"Status": 0, "TotalFrames": 4, "DroppedFrames": 1}))
		if err := c.WaitSettled(t.Context()); err != nil {
			t.Errorf("WaitSettled returned %v, want nil", err)
		}
		if s := c.State(); s.Settling.DroppedFrames != 1 || s.Settling.TotalFrames != 4 {
			t.Errorf("settling is %+v, want 4 total frames and 1 dropped", s.Settling)
		}
	})
}

// LoopingExposures sends Status as text and SettleDone sends it as a
// number, so the client must not fail on a loop event.
func TestATextStatusIsNotASettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(*phd2test.Server) {})
		f.Broadcast(phd2test.Event("LoopingExposures", map[string]any{"Frame": 1, "ErrorCode": 8, "Status": "No star selected"}))
		if s := settled(c); !s.Open || s.AppState != "Looping" || s.Settling != nil {
			t.Errorf("state after LoopingExposures is %+v, want open and Looping with no settling", s)
		}
	})
}

func TestFindStarFailsWithNoStar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(*phd2test.Server) {})
		if _, _, err := c.FindStar(t.Context()); err == nil || err.Error() != "phd2: find_star: could not find star" {
			t.Errorf("FindStar returned %v, want PHD2's no-star error", err)
		}
	})
}
