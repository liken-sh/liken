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
// step since guiding started.
type RMS struct {
	Steps          int
	RA, Dec, Total float64
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
	return out
}

// window holds the distances of the last RMSWindow guide steps.
type window struct {
	ra, dec []float64
}

func (w *window) add(ra, dec float64) RMS {
	w.ra, w.dec = append(w.ra, ra), append(w.dec, dec)
	if len(w.ra) > RMSWindow {
		w.ra, w.dec = w.ra[1:], w.dec[1:]
	}
	var sumRA, sumDec float64
	for i := range w.ra {
		sumRA += w.ra[i] * w.ra[i]
		sumDec += w.dec[i] * w.dec[i]
	}
	n := float64(len(w.ra))
	return RMS{Steps: len(w.ra), RA: math.Sqrt(sumRA / n), Dec: math.Sqrt(sumDec / n), Total: math.Sqrt((sumRA + sumDec) / n)}
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
}

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
		r.done <- err
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
// connection, so each event that implies a state sets it, the way
// PHD2's own sample clients do. A stop implies no state, because PHD2
// can stop guiding and go on looping, so a stop reads the state again.
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
		s.AppState = "Looping"
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
		s.RMS = c.rms.add(m.RADistanceRaw, m.DECDistanceRaw)
	case "StarLost":
		s.AppState = "LostLock"
	case "Paused":
		s.AppState = "Paused"
	case "GuidingStopped", "LoopingExposuresStopped":
		c.rereadLocked(methodAppState)
	case "Alert":
		// An alert follows most failures of the equipment, such as an
		// INDI camera that disconnects, and PHD2 sends no event for
		// the connection itself.
		s.Alert = &Alert{Message: m.Msg, Type: m.Type, Time: at}
		c.rereadLocked(methodCalibrated, methodConnected, methodPixelScale)
	case "ConfigurationChange":
		// A change to the profile, such as a new binning or focal
		// length, can change the pixel scale and drop the calibration.
		c.rereadLocked(methodCalibrated, methodConnected, methodPixelScale)
	}
}
