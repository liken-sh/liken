package main

import (
	"io"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A new registry has liken_build_info with the sidecar's component label
// and no decode series.
func TestScrapeHasBuildInfoWithTheCommandComponent(t *testing.T) {
	m := newCommandMetrics("2026.09.10-001")

	body := scrapeMetrics(t, m.registry)

	if want := `liken_build_info{component="media-command",version="2026.09.10-001"} 1`; !strings.Contains(body, want) {
		t.Errorf("the scrape is\n%s\nwant it to contain %q", body, want)
	}
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
}

// media_decode_info has one series at a time. A codec with no hardware
// value reads as software, and a later hwdec-current value replaces the
// series instead of adding a second.
func TestDecodeInfoHasOneSeriesAndReplacesItOnChange(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordCodec("h264")
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareNo)), 1)
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)

	m.recordHardware("vaapi")
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareYes)), 1)

	m.recordCodec("hevc")
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 1)
	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("hevc", hardwareYes)), 1)
}

// hwdec-current "no" is a software decode, so it gives hardware "false",
// the same as an unset value.
func TestDecodeInfoReadsHwdecNoAsSoftware(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordCodec("h264")
	m.recordHardware("no")

	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("h264", hardwareNo)), 1)
}

// A hardware value without a codec publishes nothing until the codec
// arrives.
func TestDecodeInfoPublishesNothingWithoutACodec(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordHardware("vaapi")

	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
}

// The first count adds in full and each later rise adds its difference,
// so the counter never falls.
func TestDroppedFramesAddsEachRiseAndNeverFalls(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordDroppedFrames(0)
	m.recordDroppedFrames(3)
	m.recordDroppedFrames(3)
	m.recordDroppedFrames(7)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 7)
}

// A count below the previous one means mpv started a new file. It adds
// nothing, and the count after it adds only the difference from it.
func TestDroppedFramesTreatsALowerCountAsANewFile(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordDroppedFrames(10)
	m.recordDroppedFrames(2) // a new file with 2 frames already dropped
	m.recordDroppedFrames(5)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 10+3)
}

// A sidecar that attaches to mpv during a file counts the frames that
// mpv already dropped.
func TestDroppedFramesCountsTheFirstCountInFull(t *testing.T) {
	m := newCommandMetrics("test")

	m.recordDroppedFrames(4)

	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 4)
}

// media_av_delay_seconds is the value of mpv's avsync property.
func TestAVDelayIsMPVsAVSync(t *testing.T) {
	m := newCommandMetrics("test")

	m.observe(changeOf(delayProperty, "-0.024"))

	mustMatch(t, testutil.ToFloat64(m.avDelay), -0.024)
}

// clearDecode deletes the decode series, sets the delay to zero, and
// resets the frame baseline.
func TestClearDecodeDropsTheSeriesAndResetsTheBaseline(t *testing.T) {
	m := newCommandMetrics("test")
	m.recordCodec("h264")
	m.recordHardware("vaapi")
	m.recordDroppedFrames(9)
	m.observe(changeOf(delayProperty, "0.5"))

	m.clearDecode()

	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)
	mustMatch(t, testutil.ToFloat64(m.avDelay), 0)

	// The baseline is zero again, so the next file's first reading
	// counts in full, the same as the very first file's did.
	m.recordDroppedFrames(4)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 9+4)
	m.recordDroppedFrames(6)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 9+4+2)
}

// observe routes each of the four properties to its metric and ignores
// a null value, which means mpv has no value yet and is not zero.
func TestObserveRoutesEachPropertyAndIgnoresNull(t *testing.T) {
	m := newCommandMetrics("test")

	m.observe(changeOf(codecProperty, "null"))
	mustMatch(t, testutil.CollectAndCount(m.decodeInfo), 0)

	m.observe(changeOf(codecProperty, `"vp9"`))
	m.observe(changeOf(hardwareProperty, `"no"`))
	m.observe(changeOf(droppedFramesProperty, "12"))
	m.observe(changeOf(delayProperty, "0.01"))

	mustMatch(t, testutil.ToFloat64(m.decodeInfo.WithLabelValues("vp9", hardwareNo)), 1)
	mustMatch(t, testutil.ToFloat64(m.droppedFrames), 12) // the first count, in full
	mustMatch(t, testutil.ToFloat64(m.avDelay), 0.01)
}

// runReporter passes the decode properties to the metrics and clears the
// decode series when the item changes.
func TestRunReporterFeedsDecodePropertiesAndClearsOnItemChange(t *testing.T) {
	metrics := newCommandMetrics("test")

	changes := make(chan propertyChange, 8)
	go feedChanges(changes,
		changeOf("playlist-pos", "0"),
		changeOf(codecProperty, `"h264"`),
		changeOf(hardwareProperty, `"vaapi"`),
		changeOf("playlist-pos", "1"),
	)
	runReporter(t.Context(), changes, func(playReport) error { return nil },
		func(int) {}, metrics, io.Discard)

	// mpv has not reported a codec for the second item, so no series
	// exists.
	mustMatch(t, testutil.CollectAndCount(metrics.decodeInfo), 0)
}
