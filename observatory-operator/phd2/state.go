package phd2

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// State is what the client knows of one PHD2. A nil field is a value
// that PHD2 has not reported on this connection.
type State struct {
	// Open is true while the event connection is open.
	Open bool
	// Version is PHD2's version, from the Version event.
	Version string
	// AppState is PHD2's state in one word: Stopped, Selected,
	// Calibrating, Guiding, LostLock, Paused, or Looping.
	AppState string
	// Calibrated is true when PHD2 holds a calibration for the mount.
	Calibrated *bool
	// Equipment is true when PHD2's camera and mount are connected.
	Equipment *bool
	// PixelScale is the guide camera's scale in arc-seconds per pixel.
	// PHD2 answers null while it does not know the camera's pixel size
	// or the focal length, and the field is nil then.
	PixelScale *float64
	// Step is the last guide step.
	Step *Step
	// RMS is the spread of the last guide steps.
	RMS RMS
	// Alert is the last alert PHD2 showed, or the last failed
	// calibration.
	Alert *Alert
	// Settling is the settle of the last Guide or Dither that this
	// client sent, or that PHD2 reported since the connection opened.
	Settling *Settling
}

// Settling is PHD2's progress toward one settle, from the SettleBegin,
// Settling, and SettleDone events.
type Settling struct {
	// Began is true after SettleBegin. A Guide that calibrates first
	// begins its settle when the calibration ends.
	Began bool
	// Distance is the star's distance from the lock position at the
	// last Settling event, in pixels.
	Distance float64
	// Time is how long the star has stayed within the settle's
	// distance, and SettleTime is how long the settle asks for.
	Time, SettleTime time.Duration
	// StarLocked is false while PHD2 has no star at the lock position.
	StarLocked bool
	// Done is true after SettleDone. Status is 0 when the star
	// settled, and Error holds PHD2's reason when it did not.
	Done   bool
	Status int
	Error  string
	// TotalFrames and DroppedFrames count the guide frames of the
	// settle, and the frames that PHD2 dropped because it lost the
	// star.
	TotalFrames, DroppedFrames int
}

// Step is one guide step, from a GuideStep event.
type Step struct {
	Frame int
	// Time is when PHD2 sent the step.
	Time time.Time
	// RA and Dec are the star's distance from the lock position along
	// the mount's axes, in pixels.
	RA, Dec float64
	// SNR is the star's signal-to-noise ratio, and HFD its half-flux
	// diameter in pixels.
	SNR, HFD float64
}

// RMSWindow is how many guide steps the RMS covers: at one step a
// second, the last 100 seconds of guiding.
const RMSWindow = 100

// RMS is the root mean square of the last guide steps' distances, in
// pixels. Steps counts the steps it covers, and is 0 before the first
// step since guiding started. PHD2 sends a new connection no earlier
// guide steps, so the window also begins at the first step that this
// connection received.
type RMS struct {
	Steps          int
	RA, Dec, Total float64
	// Since is when PHD2 sent the oldest step that the RMS covers.
	Since time.Time
}

// Alert is one alert, from an Alert event.
type Alert struct {
	Message string
	// Type is info, question, warning, or error.
	Type string
	Time time.Time
}

func (s State) clone() State {
	out := s
	if s.Step != nil {
		step := *s.Step
		out.Step = &step
	}
	if s.Alert != nil {
		alert := *s.Alert
		out.Alert = &alert
	}
	if s.Settling != nil {
		settling := *s.Settling
		out.Settling = &settling
	}
	return out
}

// window holds the distances and the times of the last RMSWindow
// guide steps.
type window struct {
	ra, dec []float64
	at      []time.Time
}

func (w *window) add(ra, dec float64, at time.Time) RMS {
	w.ra, w.dec, w.at = append(w.ra, ra), append(w.dec, dec), append(w.at, at)
	if len(w.ra) > RMSWindow {
		w.ra, w.dec, w.at = w.ra[1:], w.dec[1:], w.at[1:]
	}
	var sumRA, sumDec float64
	for i := range w.ra {
		sumRA += w.ra[i] * w.ra[i]
		sumDec += w.dec[i] * w.dec[i]
	}
	n := float64(len(w.ra))
	return RMS{Steps: len(w.ra), RA: math.Sqrt(sumRA / n), Dec: math.Sqrt(sumDec / n), Total: math.Sqrt((sumRA + sumDec) / n), Since: w.at[0]}
}

// message is any line PHD2 sends: an event has Event, and an answer
// has an id.
type message struct {
	Event  string          `json:"Event"`
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`

	Timestamp      float64 `json:"Timestamp"`
	PHDVersion     string  `json:"PHDVersion"`
	PHDSubver      string  `json:"PHDSubver"`
	State          string  `json:"State"`
	Frame          int     `json:"Frame"`
	RADistanceRaw  float64 `json:"RADistanceRaw"`
	DECDistanceRaw float64 `json:"DECDistanceRaw"`
	SNR            float64 `json:"SNR"`
	HFD            float64 `json:"HFD"`
	Msg            string  `json:"Msg"`
	Type           string  `json:"Type"`
	Reason         string  `json:"Reason"`

	Distance   float64 `json:"Distance"`
	Time       float64 `json:"Time"`
	SettleTime float64 `json:"SettleTime"`
	StarLocked bool    `json:"StarLocked"`
	// Status is a number in SettleDone and text in LoopingExposures,
	// so it is read for SettleDone alone.
	Status        json.RawMessage `json:"Status"`
	SettleError   string          `json:"Error"`
	TotalFrames   int             `json:"TotalFrames"`
	DroppedFrames int             `json:"DroppedFrames"`
}

// seconds converts a time in PHD2's seconds.
func seconds(s float64) time.Duration { return time.Duration(math.Round(s * float64(time.Second))) }

// errMethod wraps an error answer.
var errMethod = errors.New("phd2")

// receive applies one line. A line that is not JSON ends the
// connection: the stream is out of step, and a new connection reads a
// new baseline.
func (c *Client) receive(line []byte) error {
	if len(line) == 0 || line[0] == '[' {
		// The answer to a batch, which this client never sends.
		return nil
	}
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return fmt.Errorf("phd2: a message that is not JSON: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.Event != "" {
		c.event(m)
	} else if m.ID != nil {
		c.answer(*m.ID, m)
	}
	c.changeLocked()
	return nil
}

// answer applies the answer to one request. The caller holds mu.
func (c *Client) answer(id int, m message) {
	r, ok := c.pending[id]
	if !ok {
		return
	}
	delete(c.pending, id)
	var err error
	if m.Error != nil {
		err = fmt.Errorf("%w: %s: %s", errMethod, r.method, m.Error.Message)
	} else {
		c.applyRead(r.method, m.Result)
	}
	if r.done != nil {
		r.done <- reply{result: m.Result, err: err}
	}
}

// applyRead applies the result of one read. The caller holds mu.
func (c *Client) applyRead(method string, result json.RawMessage) {
	switch method {
	case methodAppState:
		var state string
		if json.Unmarshal(result, &state) == nil {
			c.state.AppState = state
		}
	case methodCalibrated:
		var calibrated bool
		if json.Unmarshal(result, &calibrated) == nil {
			c.state.Calibrated = &calibrated
		}
	case methodConnected:
		var connected bool
		if json.Unmarshal(result, &connected) == nil {
			c.state.Equipment = &connected
		}
	case methodPixelScale:
		var scale *float64
		if json.Unmarshal(result, &scale) == nil {
			c.state.PixelScale = scale
		}
	}
}

// event applies one event. PHD2 sends AppState only to a new
// connection, so each event that implies a state sets it. A stop
// implies no state, because PHD2 can stop guiding and go on looping,
// and neither does a lost lock position, so each reads the state again.
// The caller holds mu.
func (c *Client) event(m message) {
	at := time.UnixMilli(int64(math.Round(m.Timestamp * 1000))).UTC()
	s := &c.state
	switch m.Event {
	case "Version":
		s.Version = m.PHDVersion + m.PHDSubver
	case "AppState":
		s.AppState = m.State
	case "LoopingExposures":
		// PHD2 loops in two states: Looping with no star selected, and
		// Selected with one (Guider::GetExposedState in guider.cpp).
		if s.AppState != "Selected" {
			s.AppState = "Looping"
		}
	case "StarSelected":
		s.AppState = "Selected"
	case "StartCalibration", "Calibrating":
		s.AppState = "Calibrating"
		calibrated := false
		s.Calibrated = &calibrated
	case "CalibrationComplete":
		calibrated := true
		s.Calibrated = &calibrated
	case "CalibrationFailed":
		s.Alert = &Alert{Message: "Calibration failed: " + m.Reason, Type: "error", Time: at}
	case "StartGuiding":
		s.AppState = "Guiding"
		c.rms = window{}
		s.RMS = RMS{}
	case "GuideStep":
		s.AppState = "Guiding"
		s.Step = &Step{Frame: m.Frame, Time: at, RA: m.RADistanceRaw, Dec: m.DECDistanceRaw, SNR: m.SNR, HFD: m.HFD}
		s.RMS = c.rms.add(m.RADistanceRaw, m.DECDistanceRaw, at)
	case "StarLost":
		// A star lost while guiding is LostLock. One lost while
		// looping leaves a state that the event does not say.
		if s.AppState == "Guiding" || s.AppState == "LostLock" {
			s.AppState = "LostLock"
		} else {
			c.rereadLocked(methodAppState)
		}
	case "Paused":
		s.AppState = "Paused"
	case "GuidingStopped", "LoopingExposuresStopped", "LockPositionLost":
		c.rereadLocked(methodAppState)
	case "Alert":
		// An alert follows most failures of the equipment, such as an
		// INDI camera that disconnects, and PHD2 sends no event for
		// the connection itself.
		s.Alert = &Alert{Message: m.Msg, Type: m.Type, Time: at}
		c.rereadLocked(methodCalibrated, methodConnected, methodPixelScale)
	case "SettleBegin", "Settling", "SettleDone":
		c.settle(m)
	case "ConfigurationChange":
		// A change to the profile, such as a new binning or focal
		// length, can change the pixel scale and drop the calibration.
		c.rereadLocked(methodCalibrated, methodConnected, methodPixelScale)
	}
}

// settle applies one event of a settle. The settle that Guide or
// Dither recorded takes the events that follow its request. A settle
// that another client started, such as a dither from KStars, starts a
// new record at its first event, and so does a SettleBegin while a
// settle that began is still open, because PHD2 begins a new settle
// only after it ends the last one. The caller holds mu.
func (c *Client) settle(m message) {
	s := c.state.Settling
	if s == nil || s.Done || (m.Event == "SettleBegin" && s.Began) {
		s = &Settling{}
		c.state.Settling = s
	}
	s.Began = true
	switch m.Event {
	case "Settling":
		s.Distance, s.Time, s.SettleTime, s.StarLocked = m.Distance, seconds(m.Time), seconds(m.SettleTime), m.StarLocked
	case "SettleDone":
		s.Done = true
		_ = json.Unmarshal(m.Status, &s.Status)
		s.Error, s.TotalFrames, s.DroppedFrames = m.SettleError, m.TotalFrames, m.DroppedFrames
	}
}
