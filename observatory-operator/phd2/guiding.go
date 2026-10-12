package phd2

// The methods that start guiding, in the order a session calls them:
// SetExposure, Loop, FindStar, Guide, and then Dither between frames.
// observatory-operator connects PHD2's equipment and stops it, and a
// holder such as astrophotography-operator or KStars guides with it.
// Guide and Dither move the lock position or the mount, and PHD2 then
// reports a settle: SettleBegin, a Settling event for each frame, and
// SettleDone when the star stays near the lock position for the
// settle's time, or when the settle's timeout ends. A frame taken
// before SettleDone is blurred by the move, so a holder waits for it
// with WaitSettled.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrNotSettling is the error of WaitSettled when no Guide or
	// Dither of this connection is settling or has settled.
	ErrNotSettling = errors.New("phd2: no guide or dither is settling")
	// ErrSettleFailed wraps the error that SettleDone reports, such as
	// a timeout or a lost star.
	ErrSettleFailed = errors.New("phd2: the settle failed")
)

// Settle tells PHD2 when a guide or a dither has settled: when the
// star stays within Pixels of the lock position for Time. PHD2 ends the
// settle with an error after Timeout. PHD2 reads Time and Timeout as
// whole seconds (parse_settle in event_server.cpp), so the request
// rounds each one up, and a settle is never shorter than it asks.
type Settle struct {
	Pixels  float64
	Time    time.Duration
	Timeout time.Duration
}

func (s Settle) param() map[string]any {
	return map[string]any{"pixels": s.Pixels, "time": wholeSeconds(s.Time), "timeout": wholeSeconds(s.Timeout)}
}

func wholeSeconds(d time.Duration) int { return int(math.Ceil(d.Seconds())) }

// SetExposure sets the length of each guide camera exposure. PHD2
// takes it in milliseconds.
func (c *Client) SetExposure(ctx context.Context, exposure time.Duration) error {
	return c.call(ctx, "set_exposure", []any{exposure.Milliseconds()})
}

// Loop starts PHD2's exposures with no star selected, or keeps them
// going and stops guiding when it guides. The LoopingExposures events
// that follow move the state to Looping.
func (c *Client) Loop(ctx context.Context) error {
	return c.call(ctx, "loop", nil)
}

// FindStar selects the best guide star in the last frame, and answers
// its position in pixels, which becomes the lock position. PHD2 must
// be looping, and it answers an error when it finds no star.
func (c *Client) FindStar(ctx context.Context) (x, y float64, err error) {
	result, err := c.callResult(ctx, "find_star", nil)
	if err != nil {
		return 0, 0, err
	}
	var position []float64
	if err := json.Unmarshal(result, &position); err != nil || len(position) != 2 {
		return 0, 0, fmt.Errorf("%w: find_star answered %s, not a position", errMethod, result)
	}
	return position[0], position[1], nil
}

// Guide starts guiding on the selected star, and calibrates first when
// PHD2 holds no calibration or recalibrate is true. PHD2 answers once
// it starts, and reports the settle after that, so WaitSettled waits
// for the end of the calibration and the settle.
func (c *Client) Guide(ctx context.Context, settle Settle, recalibrate bool) error {
	return c.settling(ctx, "guide", map[string]any{"settle": settle.param(), "recalibrate": recalibrate})
}

// Dither moves the lock position by a random amount of up to amount
// pixels on each axis, or on the RA axis alone with raOnly, so that
// the noise of the camera falls on other pixels of the sky in the next
// frame. PHD2 must be guiding.
func (c *Client) Dither(ctx context.Context, amount float64, raOnly bool, settle Settle) error {
	return c.settling(ctx, "dither", map[string]any{"amount": amount, "raOnly": raOnly, "settle": settle.param()})
}

// settling sends a request that starts a settle. The state records a
// new settle before the request goes out, because PHD2 can send
// SettleBegin before its answer, and a SettleDone that arrives after
// that belongs to this request. A refused request starts no settle.
func (c *Client) settling(ctx context.Context, method string, params map[string]any) error {
	c.mu.Lock()
	settle := &Settling{}
	c.state.Settling = settle
	c.changeLocked()
	c.mu.Unlock()
	_, err := c.callResult(ctx, method, params)
	if err != nil {
		c.mu.Lock()
		if c.state.Settling == settle {
			c.state.Settling = nil
			c.changeLocked()
		}
		c.mu.Unlock()
	}
	return err
}

// WaitSettled waits for the SettleDone of the last Guide or Dither. It
// answers nil when the star settled, an error that wraps
// ErrSettleFailed with PHD2's reason when the settle failed,
// ErrDisconnected when the connection ends first, and ErrNotSettling
// when no settle was asked for on this connection.
func (c *Client) WaitSettled(ctx context.Context) error {
	var last State
	err := c.WaitFor(ctx, func(s State) bool {
		last = s
		return !s.Open || s.Settling == nil || s.Settling.Done
	})
	switch {
	case err != nil:
		return err
	case !last.Open:
		return ErrDisconnected
	case last.Settling == nil:
		return ErrNotSettling
	case last.Settling.Status != 0:
		return fmt.Errorf("%w: %s", ErrSettleFailed, last.Settling.Error)
	}
	return nil
}
