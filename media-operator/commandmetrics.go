package main

// The command sidecar's own Prometheus metrics, plan 26's mpv row. The
// sidecar is the one process that holds mpv's IPC socket, so the three
// series here read properties no other process ever observes, on
// baseMetrics' layer 1 alone: the sidecar's reconcile loop is mpv's own
// socket, not a watch against the API server, so it has no layer 2.

import (
	"encoding/json"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// commandBuildInfoComponent is the command sidecar's own name in
// liken_build_info, distinct from the operator's so a panel that lists
// every process in the cluster tells the two roles of this one binary
// apart.
const commandBuildInfoComponent = "media-command"

// commandMetricsPort is milestone 65's shared port for every process
// on the cluster network: a Play lives in whatever namespace a
// household put it in, so the PodMonitor that scrapes this port
// reaches across every namespace instead of sitting beside one
// Deployment the way the operator's own PodMonitor does.
const commandMetricsPort = 9200

// hardwareYes and hardwareNo are media_decode_info's hardware label.
// mpv's hwdec-current reports the decoder's own name, such as vaapi or
// nvdec, or the literal string no for a software decode, so the label
// folds that whole vocabulary down to the one question a dashboard
// asks: is this room burning CPU on a decode a GPU could do for free.
const (
	hardwareYes = "true"
	hardwareNo  = "false"
)

// commandMetrics holds the three series plan 26 gives the command
// sidecar. mpv's own socket delivers every property on its own
// goroutine, and runReporter's loop is the one reader, so the mutex
// here is only what lets a test read a value back while the loop still
// holds it.
type commandMetrics struct {
	baseMetrics

	decodeInfo    *prometheus.GaugeVec
	droppedFrames prometheus.Counter
	avDelay       prometheus.Gauge

	mu sync.Mutex
	// codec and hardware are the two properties as mpv last reported
	// them; published is what media_decode_info currently shows, which
	// lags codec and hardware until both sides of publishDecodeLocked
	// agree there is something to show.
	codec, hardware                   string
	haveCodec, haveHardware           bool
	publishedCodec, publishedHardware string
	published                         bool

	// framesBaseline is the frame-drop-count mpv reported last, zero
	// until any file has reported one. A new file resets mpv's own
	// counter, so a value under the baseline starts a new one instead
	// of subtracting, and only a rise adds to the Prometheus counter,
	// which is never allowed to fall.
	framesBaseline int64
}

// newCommandMetrics builds the command sidecar's metrics on a fresh
// registry. version is what liken_build_info reports: the operator
// reads the sidecar image's own tag and passes it down the way it
// does for the idle screen, so a mixed fleet shows the release each
// running film actually plays under.
func newCommandMetrics(version string) *commandMetrics {
	base := newBaseMetrics(commandBuildInfoComponent, version)
	m := &commandMetrics{
		baseMetrics: base,
		decodeInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "media_decode_info",
			Help: "The codec and whether hardware decodes it. Always 1 for the current combination.",
		}, []string{"codec", "hardware"}),
		droppedFrames: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "media_dropped_frames_total",
			Help: "Frames mpv reports dropped, summed across every file this run has played.",
		}),
		avDelay: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_av_delay_seconds",
			Help: "mpv's own audio-to-video offset. Positive is audio ahead of video.",
		}),
	}
	base.registry.MustRegister(m.decodeInfo, m.droppedFrames, m.avDelay)
	return m
}

// observe folds one property change from mpv's socket into whichever
// series it belongs to. A change readEvents never delivers for an
// unobserved property, and a null value before mpv knows an answer,
// both leave every series exactly as they were.
func (m *commandMetrics) observe(change propertyChange) {
	if !change.known() {
		return
	}
	switch change.Name {
	case codecProperty:
		var codec string
		if json.Unmarshal(change.Data, &codec) == nil {
			m.noteCodec(codec)
		}
	case hardwareProperty:
		var hwdec string
		if json.Unmarshal(change.Data, &hwdec) == nil {
			m.noteHardware(hwdec)
		}
	case droppedFramesProperty:
		var count int64
		if json.Unmarshal(change.Data, &count) == nil {
			m.noteDroppedFrames(count)
		}
	case delayProperty:
		var seconds float64
		if json.Unmarshal(change.Data, &seconds) == nil {
			m.avDelay.Set(seconds)
		}
	}
}

// noteCodec records mpv's current-tracks/video/codec and republishes
// media_decode_info if the published series no longer matches.
func (m *commandMetrics) noteCodec(codec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codec, m.haveCodec = codec, true
	m.publishDecodeLocked()
}

// noteHardware records mpv's hwdec-current and republishes
// media_decode_info if the published series no longer matches.
func (m *commandMetrics) noteHardware(hwdec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hardware, m.haveHardware = hwdec, true
	m.publishDecodeLocked()
}

// publishDecodeLocked resets media_decode_info to the codec and
// hardware this run now holds, so exactly one label combination reads
// 1 at a time, the way an info gauge must. The caller holds mu. It
// waits for a codec before it publishes anything, because a hardware
// reading with no codec names nothing a dashboard could show; an
// unset hwdec-current reads as software, since mpv reports no before
// it ever reports a decoder's name.
func (m *commandMetrics) publishDecodeLocked() {
	if !m.haveCodec {
		return
	}
	hardware := hardwareNo
	if m.haveHardware && m.hardware != "" && m.hardware != "no" {
		hardware = hardwareYes
	}
	if m.published && m.publishedCodec == m.codec && m.publishedHardware == hardware {
		return
	}
	if m.published {
		m.decodeInfo.DeleteLabelValues(m.publishedCodec, m.publishedHardware)
	}
	m.decodeInfo.WithLabelValues(m.codec, hardware).Set(1)
	m.publishedCodec, m.publishedHardware, m.published = m.codec, hardware, true
}

// noteDroppedFrames folds mpv's own running count into the total this
// counter has added since the baseline last reset. The baseline starts
// at zero, so the first count any file reports adds in full: mpv
// answers observe_property with the current value at once, and a
// title already mid-decode by the time the sidecar attaches has
// really dropped whatever it reports.
//
// mpv resets its own count to zero on every new file in the playlist,
// so a value under the last one read is not a fact to add against it;
// it is never subtracted, and it becomes the baseline the next file's
// deltas measure from instead.
func (m *commandMetrics) noteDroppedFrames(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if count > m.framesBaseline {
		m.droppedFrames.Add(float64(count - m.framesBaseline))
	}
	m.framesBaseline = count
}

// clearDecode drops the published decode series and resets the delay
// gauge, on the two moments this run's mpv facts stop describing
// anything live: the file it was about ends, and the socket to mpv
// drops. The frame baseline resets too, so the file that follows
// starts its own count instead of measuring against one that is gone.
func (m *commandMetrics) clearDecode() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.published {
		m.decodeInfo.DeleteLabelValues(m.publishedCodec, m.publishedHardware)
	}
	m.codec, m.hardware = "", ""
	m.haveCodec, m.haveHardware = false, false
	m.publishedCodec, m.publishedHardware, m.published = "", "", false
	m.framesBaseline = 0
	m.avDelay.Set(0)
}
