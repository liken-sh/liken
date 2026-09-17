package main

// This file is the client one liken API uses to call another. It
// sends the token projected for the sibling's audience under this
// API's own ServiceAccount, dials through the sibling's published CA,
// and turns each answer the sibling can give into an open stream or
// a fault this API's own client can read: the sibling's problem
// document relayed under a mapped status, with its own words as the
// detail.

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The two bounds on an upstream leg. The header bound is 10 s, plus
// the caller's begin where there is one, then 504. It is a variable
// so a test drives it in milliseconds.
var upstreamHeaderTimeout = 10 * time.Second

// The idle bound on an upstream body is 30 s, counted from the first
// body byte or from begin, whichever is later. It is a variable so a
// test drives it in milliseconds.
var upstreamIdleTimeout = 30 * time.Second

// upstreamStream is one open upstream body with the instant its
// headers arrived on this API's clock. That instant is the whole of
// the offset correction: the difference between two streams' instants
// is the -itsoffset the mux applies.
type upstreamStream struct {
	name      string
	target    string
	headersAt time.Time
	body      io.ReadCloser
	codecs    string
	cancel    context.CancelFunc
}

func (s *upstreamStream) close() {
	if s == nil {
		return
	}
	if s.body != nil {
		_ = s.body.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
}

// upstreamFault is what an upstream answer that is not a stream
// becomes for the client of this API: the mapped status, the problem
// type and title, the upstream's own words, its URL, and its
// Retry-After where it sent one.
type upstreamFault struct {
	status     int
	kind       string
	title      string
	detail     string
	upstream   string
	retryAfter string
}

// upstreamClient holds what a call to a sibling needs: the HTTP
// client that dials through the sibling anchors, the token for each
// audience, the metrics, and the clock that stamps header arrival.
type upstreamClient struct {
	http    *http.Client
	token   func(upstream string) (string, error)
	metrics *apiMetrics
	clock   func(upstream string) time.Time
}

// open sends one upstream request and answers the stream or the
// fault. The header bound is a timer that cancels the request's
// context, not a client timeout, because a client timeout covers the
// whole exchange and would cut a live stream on schedule. The timer
// stops once the headers arrive, and the body runs under the idle
// bound instead.
func (u *upstreamClient) open(parent context.Context, name, target string, headerTimeout time.Duration) (*upstreamStream, *upstreamFault) {
	ctx, cancel := context.WithCancel(parent)
	var late atomic.Bool
	timer := time.AfterFunc(headerTimeout, func() {
		late.Store(true)
		cancel()
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, &upstreamFault{
			status: http.StatusBadGateway, kind: problemUpstreamFailed,
			title: "The upstream request could not be built", detail: err.Error(), upstream: target,
		}
	}
	token, err := u.token(name)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, &upstreamFault{
			status: http.StatusServiceUnavailable, kind: aboutBlank,
			detail: err.Error(), upstream: target, retryAfter: retryAfterSeconds,
		}
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := u.http.Do(request)
	timer.Stop()
	if err != nil {
		cancel()
		if late.Load() {
			u.metrics.observeUpstream(name, "timeout")
			return nil, &upstreamFault{
				status: http.StatusGatewayTimeout, kind: aboutBlank,
				detail:   "the upstream sent no headers within " + headerTimeout.String(),
				upstream: target,
			}
		}
		u.metrics.observeUpstream(name, "error")
		return nil, &upstreamFault{
			status: http.StatusServiceUnavailable, kind: aboutBlank,
			detail: err.Error(), upstream: target, retryAfter: retryAfterSeconds,
		}
	}
	u.metrics.observeUpstream(name, strconv.Itoa(response.StatusCode))
	if response.StatusCode != http.StatusOK {
		fault := relayFault(response, target)
		drain(response.Body)
		cancel()
		return nil, fault
	}
	return &upstreamStream{
		name:      name,
		target:    target,
		headersAt: u.clock(name),
		body:      response.Body,
		codecs:    codecsOf(response.Header.Get("Content-Type")),
		cancel:    cancel,
	}, nil
}

// relayFault maps a sibling's status to this API's own: a 400 and a
// 404 relay as themselves with the upstream's problem as the detail,
// a 503 relays as capture-busy with the upstream's Retry-After, and
// any other status is a 502. An answer that is not a problem document
// is a 502 whatever its status, because this API cannot say what it
// means and must not pass it off as its own.
func relayFault(response *http.Response, target string) *upstreamFault {
	detail, problem := upstreamDetail(response)
	switch {
	case !problem:
		return &upstreamFault{
			status: http.StatusBadGateway, kind: problemUpstreamFailed,
			title:  "The upstream answered something this API cannot relay",
			detail: detail, upstream: target,
		}
	case response.StatusCode == http.StatusBadRequest:
		return &upstreamFault{
			status: http.StatusBadRequest, kind: aboutBlank,
			detail: detail, upstream: target,
		}
	case response.StatusCode == http.StatusNotFound:
		return &upstreamFault{
			status: http.StatusNotFound, kind: aboutBlank,
			detail: detail, upstream: target,
		}
	case response.StatusCode == http.StatusServiceUnavailable:
		after := response.Header.Get("Retry-After")
		if after == "" {
			after = retryAfterSeconds
		}
		return &upstreamFault{
			status: http.StatusServiceUnavailable, kind: problemCaptureBusy,
			title: "The upstream capture is busy", detail: detail,
			upstream: target, retryAfter: after,
		}
	default:
		return &upstreamFault{
			status: http.StatusBadGateway, kind: problemUpstreamFailed,
			title:  "The upstream answered a status this API cannot relay",
			detail: detail, upstream: target,
		}
	}
}

// upstreamDetail reads the sibling's own words out of its answer, this
// repository's error rule: the problem document's detail, or its
// title when the detail is empty. An answer that is not a problem
// document gives its status line and body as the words, and false.
func upstreamDetail(response *http.Response) (string, bool) {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind != problemContentType {
		return strings.TrimSpace(response.Status + " " + string(body)), false
	}
	var document problemDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return strings.TrimSpace(string(body)), false
	}
	if document.Detail == "" {
		return document.Title, true
	}
	return document.Detail, true
}

// codecsOf reads the avc1 element out of the display upstream's
// Content-Type, which is where the video half of the composed codecs
// parameter comes from.
func codecsOf(contentType string) string {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	for _, codec := range strings.Split(parameters["codecs"], ",") {
		codec = strings.TrimSpace(codec)
		if strings.HasPrefix(codec, "avc1") {
			return codec
		}
	}
	return ""
}
