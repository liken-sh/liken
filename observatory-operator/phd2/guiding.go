package phd2

// A client that guides, such as astrophotography-operator, calls
// SetExposure, Loop, FindStar, and Guide, and then Dither between
// frames. observatory-operator only connects PHD2's equipment and
// stops it. After Guide or Dither, PHD2 sends SettleBegin, a Settling
// event for each frame, and SettleDone when the star has stayed near
// the lock position for the settle's time or the settle's timeout ends.
// A frame taken before SettleDone is blurred by the move, so the client
// calls WaitSettled before it takes the next frame.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrNotSettling is what WaitSettled returns when no settle has
	// started on this connection.
	ErrNotSettling = errors.New("phd2: no guide or dither is settling")
	// ErrSettleFailed wraps the error in SettleDone, such as a timeout
	// or a lost star.
	ErrSettleFailed = errors.New("phd2: the guide or dither did not settle")
)

// Settle tells PHD2 when a guide or a dither counts as settled: when
// the star has stayed within Pixels of the lock position for Time. PHD2
// ends the settle with an error after Timeout. PHD2 reads Time and
// Timeout as whole seconds (parse_settle in event_server.cpp), so the
// request rounds each one up.
type Settle struct {
	Pixels  float64
	Time    time.Duration
	Timeout time.Duration
}

func (s Settle) param() map[string]any {
	return map[string]any{"pixels": s.Pixels, "time": wholeSeconds(s.Time), "timeout": wholeSeconds(s.Timeout)}
}

func wholeSeconds(d time.Duration) int { return int(math.Ceil(d.Seconds())) }

// SetExposure sets the length of each guide camera exposure.
func (c *Client) SetExposure(ctx context.Context, exposure time.Duration) error {
	return c.call(ctx, "set_exposure", []any{exposure.Milliseconds()})
}

// Loop starts PHD2's exposures with no star selected. When PHD2 is
// guiding, Loop stops the guiding and keeps the exposures running.
// The LoopingExposures events that follow move the state to Looping.
func (c *Client) Loop(ctx context.Context) error {
	return c.call(ctx, "loop", nil)
}

// FindStar selects the best guide star in the last frame and returns
// its position in pixels, which becomes the lock position. PHD2 must
// be looping, and FindStar fails when PHD2 finds no star.
func (c *Client) FindStar(ctx context.Context) (x, y float64, err error) {
	result, err := c.callResult(ctx, "find_star", nil)
	if err != nil {
		return 0, 0, err
	}
	var position []float64
	if err := json.Unmarshal(result, &position); err != nil || len(position) != 2 {
		return 0, 0, fmt.Errorf("%w: find_star returned %s, want a position as two numbers", errMethod, result)
	}
	return position[0], position[1], nil
}

// Guide starts guiding on the selected star. It calibrates first when
// PHD2 has no calibration or recalibrate is true. PHD2 responds before
// it calibrates and settles, so the caller waits for both with
// WaitSettled.
func (c *Client) Guide(ctx context.Context, settle Settle, recalibrate bool) error {
	return c.settling(ctx, "guide", map[string]any{"settle": settle.param(), "recalibrate": recalibrate})
}

// Dither moves the lock position by a random amount of up to amount
// pixels on each axis, or on the RA axis alone with raOnly. The next
// frame then puts the stars on different pixels, so the camera's fixed
// noise does not repeat at one place in the sky. PHD2 must be guiding.
func (c *Client) Dither(ctx context.Context, amount float64, raOnly bool, settle Settle) error {
	return c.settling(ctx, "dither", map[string]any{"amount": amount, "raOnly": raOnly, "settle": settle.param()})
}

// settling sends a request that starts a settle. It creates the
// request's Settling before it sends the request, because PHD2 can send
// SettleBegin before its response, and the settle events must update
// this Settling. When PHD2 refuses the request, settling removes the
// Settling again, so WaitSettled does not wait for a SettleDone that
// never comes.
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

// WaitSettled waits for the SettleDone of the current settle, which
// another client can also have started. It returns nil when the star settled, an error that
// wraps ErrSettleFailed with PHD2's reason when the settle failed,
// ErrDisconnected when the connection ends first, and ErrNotSettling
// when no settle has started on this connection.
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
