package main

// These tests cover the api role's own series: every observer answers
// on a nil receiver, and each one records what its name says under the
// labels the plan's table states.

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// A test builds no registry where it has no use for one, so every
// observer must answer on a nil receiver rather than panic.
func TestTheApiSeriesAnswerOnANilReceiver(t *testing.T) {
	var absent *apiMetrics

	absent.observeRequest(apiPlayerTemplate, "GET", 200, time.Second)
	absent.observeUpstream(upstreamDisplay, "200")
	absent.observeOffset(0.04)
	absent.observeExpiry(3600)
	absent.holdStream(mediaAspectName)()
}

func TestTheApiSeriesRecordWhatTheyName(t *testing.T) {
	metrics := newAPIMetrics("test")

	metrics.observeRequest(apiPlayerTemplate, "GET", 200, 250*time.Millisecond)
	metrics.observeUpstream(upstreamDisplay, "503")
	metrics.observeOffset(0.04)
	metrics.observeExpiry(3600)
	release := metrics.holdStream(mediaAspectName)

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
		"media_api_compose_offset_seconds",
		"media_api_request_seconds",
		"media_api_requests_total",
		"media_api_streams_active",
		"media_api_upstream_requests_total",
	})
}
