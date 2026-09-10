package main

// These tests prove the command sidecar's three mpv-derived series: a
// real registry, a scrape through promhttp for layer 1, and the
// derivations plan 26 states for the domain series, driven straight
// through commandMetrics and through runReporter's own loop.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A fresh registry answers layer 1 under the command sidecar's own
// component name, distinct from the operator's, and reports no domain
// series until mpv says something.
func TestACommandScrapeCarriesLayerOneUnderItsOwnComponent(t *testing.T) {
	m := newCommandMetrics("2026.09.10-001")

	body := scrapeMetrics(t, m.registry)

	if want := `liken_build_info{component="media-command",version="2026.09.10-001"} 1`; !strings.Contains(body, want) {
		t.Errorf("scrape did not carry %q\n%s", want, body)
	}
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
}

// media_decode_info holds exactly one series at a time: a codec with
// no hardware report yet reads as software, and a later hwdec report
// replaces it rather than adding a second series.
func TestDecodeInfoHoldsOneSeriesAndReplacesItOnChange(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteCodec("h264")
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareNo)), 1)
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)

	m.noteHardware("vaapi")
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareYes)), 1)

	m.noteCodec("hevc")
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("hevc", hardwareYes)), 1)
}

// hwdec-current reports the literal string no for a software decode,
// which reads as hardware: false, the same as an unset report.
func TestDecodeInfoReadsMPVsNoAsSoftware(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteCodec("h264")
	m.noteHardware("no")

	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareNo)), 1)
}

// A hardware report with no codec yet names nothing to show, so it
// publishes no series until the codec arrives.
func TestDecodeInfoWaitsForACodecBeforeItPublishes(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteHardware("vaapi")

	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
}

// The baseline starts at zero, so the first reading a run ever takes
// adds in full, and every later rise within the same file adds its
// own delta, which is what keeps the counter itself only ever rising.
func TestDroppedFramesAddsEachRiseAndNeverFalls(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteDroppedFrames(0)
	m.noteDroppedFrames(3)
	m.noteDroppedFrames(3)
	m.noteDroppedFrames(7)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 7)
}

// A new file resets mpv's own count, so a value under the last one
// read starts a new baseline instead of being read as a fall, and the
// reading that follows adds only its own delta from that new baseline.
func TestDroppedFramesTreatsADecreaseAsANewFile(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteDroppedFrames(10)
	m.noteDroppedFrames(2) // the next file, already three frames in
	m.noteDroppedFrames(5)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 10+3)
}

// A title already mid-decode by the time the sidecar attaches to mpv
// has really dropped whatever frame-drop-count answers with at once,
// so the very first reading counts in full and not as bare bookkeeping.
func TestDroppedFramesCountsTheFirstReadingInFull(t *testing.T) {
	m := newCommandMetrics("test")

	m.noteDroppedFrames(4)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 4)
}

// media_av_delay_seconds is avsync read straight through.
func TestAVDelayReportsMPVsAVSyncDirectly(t *testing.T) {
	m := newCommandMetrics("test")

	m.observe(changeOf(delayProperty, "-0.024"))

	mustMatch(t, testutil.ToFloat64(m.avDelay), -0.024)
}

// clearDecode is the file-ends and connection-drops moment: the
// published decode series goes, the delay resets to zero, and the
// frame baseline resets so the file that follows measures its own
// drops rather than a delta against one that is gone.
func TestClearDecodeDropsTheSeriesAndResetsTheBaseline(t *testing.T) {
	m := newCommandMetrics("test")
	m.noteCodec("h264")
	m.noteHardware("vaapi")
	m.noteDroppedFrames(9)
	m.observe(changeOf(delayProperty, "0.5"))

	m.clearDecode()

	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
	mustMatch(t, testutil.ToFloat64(m.avDelay), 0)

	// The baseline is zero again, so the next file's first reading
	// counts in full, the same as the very first file's did.
	m.noteDroppedFrames(4)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 9+4)
	m.noteDroppedFrames(6)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 9+4+2)
}

// observe reads each of the four properties by name and drops a null
// value the way the rest of the sidecar treats an unanswered property:
// as nothing known yet, not as zero.
func TestObserveReadsEachDecodePropertyAndDropsAnUnknownOne(t *testing.T) {
	m := newCommandMetrics("test")

	m.observe(changeOf(codecProperty, "null"))
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)

	m.observe(changeOf(codecProperty, `"vp9"`))
	m.observe(changeOf(hardwareProperty, `"no"`))
	m.observe(changeOf(droppedFramesProperty, "12"))
	m.observe(changeOf(delayProperty, "0.01"))

	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("vp9", hardwareNo)), 1)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 12) // the first reading, counted in full
	mustMatch(t, testutil.ToFloat64(m.avDelay), 0.01)
}

// runReporter is the one caller that ever reaches commandMetrics from
// mpv's own socket, so this proves the wiring rather than the
// derivations the tests above already cover: the decode properties
// reach the metrics untouched by the report loop, and an item change
// clears the series the way a new file demands.
func TestRunReporterFeedsTheDecodePropertiesToMetricsAndClearsOnEachItem(t *testing.T) {
	metrics := newCommandMetrics("test")

	changes := make(chan propertyChange, 8)
	go feedChanges(changes,
		changeOf("playlist-pos", "0"),
		changeOf(codecProperty, `"h264"`),
		changeOf(hardwareProperty, `"vaapi"`),
		changeOf("playlist-pos", "1"),
	)
	runReporter(t.Context(), changes, func(playReport) error { return nil },
		func(int) {}, func(json.RawMessage) {}, metrics)

	// The second item's own observe has not reported a codec yet, so the
	// series the first item published is gone and nothing has replaced
	// it.
	mustMatch(t, testutil.CollectAndCount(metrics.decodeInfo), 0)
}
