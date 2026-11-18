package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestTheApiSeriesDoNothingOnANilReceiver(t *testing.T) {
	var absent *apiMetrics

	absent.observeRequest(apiPlayerTemplate, "GET", 200, time.Second)
	absent.observeUpstream(upstreamDisplay, "200")
	absent.observeOffset(0.04)
	absent.observeExpiry(3600)
	absent.trackStream(mediaAspectName)()
}

func TestTheApiSeriesRecordTheirObservations(t *testing.T) {
	metrics := newAPIMetrics("test")

	metrics.observeRequest(apiPlayerTemplate, "GET", 200, 250*time.Millisecond)
	metrics.observeUpstream(upstreamDisplay, "503")
	metrics.observeOffset(-0.04)
	metrics.observeExpiry(3600)
	release := metrics.trackStream(mediaAspectName)

	mustMatch(t, testutil.ToFloat64(metrics.streamsActive.WithLabelValues(mediaAspectName)), 1.0)
	release()
	mustMatch(t, testutil.ToFloat64(metrics.streamsActive.WithLabelValues(mediaAspectName)), 0.0)
	mustMatch(t, testutil.ToFloat64(metrics.certificateExpiry), 3600.0)
	mustMatch(t, testutil.ToFloat64(
		metrics.upstreamRequests.WithLabelValues(upstreamDisplay, "503")), 1.0)

	gathered, err := metrics.registry.Gather()
	mustSucceed(t, err)
	var names []string
	for _, family := range gathered {
		if strings.HasPrefix(family.GetName(), "media_api_") {
			names = append(names, family.GetName())
		}
	}
	mustMatchAll(t, names, []string{
		"media_api_certificate_expiry_seconds",
		"media_api_compose_offset_last_seconds",
		"media_api_compose_offset_seconds",
		"media_api_request_seconds",
		"media_api_requests_total",
		"media_api_streams_active",
		"media_api_upstream_requests_total",
	})
}

func TestTheOffsetHistogramRecordsTheSizeAndTheGaugeTheSign(t *testing.T) {
	metrics := newAPIMetrics("test")

	metrics.observeOffset(-0.86)
	metrics.observeOffset(0.02)

	mustMatch(t, testutil.ToFloat64(metrics.composeOffsetLast), 0.02)
	mustMatchAll(t, offsetBuckets(t, metrics), []string{
		"0.001:0", "0.005:0", "0.01:0", "0.033:1", "0.1:1",
		"0.25:1", "0.5:1", "1:2", "2:2",
	})
}

// offsetBuckets returns each bucket of the offset histogram as
// "upper bound:cumulative count".
func offsetBuckets(t *testing.T, metrics *apiMetrics) []string {
	t.Helper()
	gathered, err := metrics.registry.Gather()
	mustSucceed(t, err)
	var read []string
	for _, family := range gathered {
		if family.GetName() != "media_api_compose_offset_seconds" {
			continue
		}
		for _, bucket := range family.GetMetric()[0].GetHistogram().GetBucket() {
			read = append(read, strconv.FormatFloat(bucket.GetUpperBound(), 'g', -1, 64)+
				":"+strconv.FormatUint(bucket.GetCumulativeCount(), 10))
		}
	}
	return read
}
