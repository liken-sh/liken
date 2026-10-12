package phd2

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/observatory-operator/phd2/phd2test"
)

// guiding answers a client of a fake PHD2 whose equipment is connected,
// once the client has read its baseline.
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
			t.Fatal(err)
		}
		if f.Exposure != 1500 {
			t.Errorf("exposure = %d ms", f.Exposure)
		}
	})
}

func TestLoopFindStarAndGuideSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(*phd2test.Server) {})
		if err := c.Loop(t.Context()); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); s.AppState != "Looping" {
			t.Errorf("after loop = %q", s.AppState)
		}
		x, y, err := c.FindStar(t.Context())
		if err != nil || x != 1045.09 || y != 44.96 {
			t.Errorf("find_star = %v, %v, %v", x, y, err)
		}
		if err := c.Guide(t.Context(), aSettle, false); err != nil {
			t.Fatal(err)
		}
		if err := c.WaitSettled(t.Context()); err != nil {
			t.Errorf("settle = %v", err)
		}
		s := settled(c)
		if s.AppState != "Guiding" || s.Calibrated == nil || !*s.Calibrated {
			t.Errorf("after guide = %+v", s)
		}
		want := Settling{Began: true, Distance: 0.67, SettleTime: 5 * time.Second, StarLocked: true, Done: true, TotalFrames: 7}
		if *s.Settling != want {
			t.Errorf("settle = %+v, want %+v", *s.Settling, want)
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
			t.Fatal(err)
		}
		want := `{"amount":3,"raOnly":true,"settle":{"pixels":0.8,"time":5,"timeout":30}}`
		if got := string(f.Params("dither")); got != want {
			t.Errorf("params = %s, want %s", got, want)
		}
	})
}

func TestAFailedSettleAnswersPHD2sReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(f *phd2test.Server) {
			f.AppState, f.SettleFailure = "Guiding", "timed-out waiting for guider to settle"
		})
		if err := c.Dither(t.Context(), 5, false, aSettle); err != nil {
			t.Fatal(err)
		}
		err := c.WaitSettled(t.Context())
		if !errors.Is(err, ErrSettleFailed) || err.Error() != "phd2: the settle failed: timed-out waiting for guider to settle" {
			t.Errorf("settle = %v", err)
		}
		if s := c.State(); s.Settling.Status != 1 {
			t.Errorf("status = %d", s.Settling.Status)
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
			t.Errorf("dither = %v", err)
		}
		if err := c.WaitSettled(t.Context()); !errors.Is(err, ErrNotSettling) {
			t.Errorf("wait = %v", err)
		}
	})
}

// The settle reports its progress while it runs, and a wait ends when
// the connection does.
func TestASettleInProgressAndAConnectionThatEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(f *phd2test.Server) { f.AppState, f.HoldSettle = "Guiding", true })
		if err := c.Dither(t.Context(), 5, false, aSettle); err != nil {
			t.Fatal(err)
		}
		if s := settled(c); !s.Settling.Began || s.Settling.Done || s.Settling.Distance != 0.67 {
			t.Errorf("settle = %+v", s.Settling)
		}
		ended := make(chan error, 1)
		go func() { ended <- c.WaitSettled(t.Context()) }()
		synctest.Wait()
		f.Cut()
		if err := <-ended; !errors.Is(err, ErrDisconnected) {
			t.Errorf("wait = %v", err)
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
			t.Errorf("settle = %+v", s.Settling)
		}
		f.Broadcast(phd2test.Event("SettleBegin", nil))
		if s := settled(c); s.Settling.Distance != 0 || !s.Settling.Began {
			t.Errorf("settle after a new SettleBegin = %+v", s.Settling)
		}
		f.Broadcast(phd2test.Event("SettleDone", map[string]any{"Status": 0, "TotalFrames": 4, "DroppedFrames": 1}))
		if err := c.WaitSettled(t.Context()); err != nil {
			t.Errorf("wait = %v", err)
		}
		if s := c.State(); s.Settling.DroppedFrames != 1 || s.Settling.TotalFrames != 4 {
			t.Errorf("settle = %+v", s.Settling)
		}
	})
}

// LoopingExposures carries Status as text, and SettleDone as a number,
// so a loop's event never ends the connection.
func TestATextStatusIsNotASettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := guiding(t, func(*phd2test.Server) {})
		f.Broadcast(phd2test.Event("LoopingExposures", map[string]any{"Frame": 1, "ErrorCode": 8, "Status": "No star selected"}))
		if s := settled(c); !s.Open || s.AppState != "Looping" || s.Settling != nil {
			t.Errorf("state = %+v", s)
		}
	})
}

func TestFindStarFailsWithNoStar(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := guiding(t, func(*phd2test.Server) {})
		if _, _, err := c.FindStar(t.Context()); err == nil || err.Error() != "phd2: find_star: could not find star" {
			t.Errorf("find_star = %v", err)
		}
	})
}
