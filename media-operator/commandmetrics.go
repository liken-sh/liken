package main

// The command sidecar serves the base metrics and the metrics that only
// mpv's IPC socket can give: the decoder, dropped frames, and the A/V
// offset. The sidecar does not watch the API server, so it serves none
// of the operator's other metrics.

import (
	"encoding/json"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// commandBuildInfoComponent is the sidecar's component label in
// liken_build_info. The operator and the sidecar are the same binary,
// so a dashboard tells them apart by this label.
const commandBuildInfoComponent = "media-command"

// commandMetricsPort is the port that the sidecar serves /metrics on,
// the same port as the operator, the API, and the idle pod.
const commandMetricsPort = 9200

// hardwareYes and hardwareNo are the values of the hardware label of
// media_decode_info. mpv's hwdec-current is the name of a hardware
// decoder, such as vaapi or nvdec, or "no" for a software decode. The
// label keeps only whether a GPU decodes the video.
const (
	hardwareYes = "true"
	hardwareNo  = "false"
)

// commandMetrics holds the metrics that the sidecar computes from mpv's
// properties. observe and clearDecode run on different goroutines, so
// mu covers the fields below it.
type commandMetrics struct {
	baseMetrics

	decodeInfo    *prometheus.GaugeVec
	droppedFrames prometheus.Counter
	avDelay       prometheus.Gauge

	mu sync.Mutex
	// codec and hardware are the last values mpv reported.
	// publishedCodec and publishedHardware are the labels of the series
	// that media_decode_info has now, when published is true.
	codec, hardware                   string
	haveCodec, haveHardware           bool
	publishedCodec, publishedHardware string
	published                         bool

	// framesBaseline is the last frame-drop-count that mpv reported, or
	// zero before any.
	framesBaseline int64
}

// newCommandMetrics returns the sidecar's metrics on a new registry.
// version is the sidecar image's tag, which the operator passes to the
// sidecar, so liken_build_info shows the release that each Play runs.
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

// observe updates the metric of the changed property. It ignores a null
// value, which mpv sends for a property that has no value yet.
func (m *commandMetrics) observe(change propertyChange) {
	if !change.known() {
		return
	}
	switch change.Name {
	case codecProperty:
		var codec string
		if json.Unmarshal(change.Data, &codec) == nil {
			m.recordCodec(codec)
		}
	case hardwareProperty:
		var hwdec string
		if json.Unmarshal(change.Data, &hwdec) == nil {
			m.recordHardware(hwdec)
		}
	case droppedFramesProperty:
		var count int64
		if json.Unmarshal(change.Data, &count) == nil {
			m.recordDroppedFrames(count)
		}
	case delayProperty:
		var seconds float64
		if json.Unmarshal(change.Data, &seconds) == nil {
			m.avDelay.Set(seconds)
		}
	}
}

func (m *commandMetrics) recordCodec(codec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codec, m.haveCodec = codec, true
	m.publishDecodeLocked()
}

func (m *commandMetrics) recordHardware(hwdec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hardware, m.haveHardware = hwdec, true
	m.publishDecodeLocked()
}

// publishDecodeLocked replaces the series of media_decode_info with one
// for the current codec and hardware, so the info metric has one series
// at a time. The caller holds mu. It publishes nothing until mpv reports
// a codec, because the hardware label alone tells a dashboard nothing.
// A hardware value that is unset, empty, or "no" counts as software.
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

// recordDroppedFrames adds the frames dropped since the previous count to
// media_dropped_frames_total. The baseline starts at zero, so when the
// sidecar connects during a file, the frames that mpv dropped before it
// connected count too.
//
// mpv resets its count to zero for each new file. A count below the
// baseline adds nothing and becomes the new baseline, because a
// Prometheus counter must not fall.
func (m *commandMetrics) recordDroppedFrames(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if count > m.framesBaseline {
		m.droppedFrames.Add(float64(count - m.framesBaseline))
	}
	m.framesBaseline = count
}

// clearDecode deletes the decode series, sets the delay to zero, and
// resets the frame baseline. The sidecar calls it when the playlist
// moves to another item and when the connection to mpv closes, because
// the values describe the file that mpv was playing.
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
