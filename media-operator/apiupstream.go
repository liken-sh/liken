package main

// This file is the client that this API uses to call the display and
// audio APIs, called the upstreams here. Each request carries a bearer
// token that Kubernetes projects for the upstream's audience into a
// file of this pod. A 200 response becomes an open stream, and any other
// response becomes an upstreamFault, which the caller returns to its own
// client as a problem document.

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// upstreamHeaderTimeout is how long an upstream has to send its response
// headers, plus the request's begin offset. A late upstream becomes a
// 504. It is a variable so that a test that streams through a stand-in
// ffmpeg, which runs on the real clock, can shorten it.
var upstreamHeaderTimeout = 10 * time.Second

// upstreamIdleTimeout is how long an upstream body may go without
// sending data, counted from the first body byte or from begin,
// whichever is later.
var upstreamIdleTimeout = 30 * time.Second

// upstreamStream is an open upstream response body. headersAt is when
// the headers arrived. The composition handler passes the difference
// between two streams' headersAt values to ffmpeg as -itsoffset, so
// the inputs line up in time.
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

// upstreamFault is the problem document that this API returns for an
// upstream failure. upstream is the upstream's URL.
type upstreamFault struct {
	status     int
	kind       string
	title      string
	detail     string
	upstream   string
	retryAfter string
}

// upstreamClient calls upstream APIs. token returns the bearer token
// for an upstream, and clock returns the headersAt time of a stream.
type upstreamClient struct {
	http    *http.Client
	token   func(upstream string) (string, error)
	metrics *apiMetrics
	clock   func(upstream string) time.Time
}

// open sends a GET to target and returns the open stream, or a fault
// when the request fails or the headers do not arrive within
// headerTimeout. The deadline is a timer that cancels the request
// context. http.Client.Timeout would also limit the body and cut off a
// live stream. After the headers arrive, the caller's idle watchdog
// limits the body.
func (u *upstreamClient) open(parent context.Context, name, target string, headerTimeout time.Duration) (*upstreamStream, *upstreamFault) {
	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(headerTimeout, cancel)
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
	inTime := timer.Stop()
	if err != nil {
		cancel()
		if !inTime {
			return nil, u.lateFault(name, target, headerTimeout)
		}
		u.metrics.observeUpstream(name, "error")
		return nil, &upstreamFault{
			status: http.StatusServiceUnavailable, kind: aboutBlank,
			detail: err.Error(), upstream: target, retryAfter: retryAfterSeconds,
		}
	}
	// When the deadline fires just as the headers arrive, the context is
	// cancelled and the first body read would fail, so return the 504
	// now.
	if !inTime {
		drain(response.Body)
		cancel()
		return nil, u.lateFault(name, target, headerTimeout)
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
		codecs:    upstreamCodecs(response.Header.Get("Content-Type")),
		cancel:    cancel,
	}, nil
}

// lateFault returns the 504 for an upstream that did not send its
// headers within headerTimeout.
func (u *upstreamClient) lateFault(name, target string, headerTimeout time.Duration) *upstreamFault {
	u.metrics.observeUpstream(name, "timeout")
	return &upstreamFault{
		status: http.StatusGatewayTimeout, kind: aboutBlank,
		detail:   "the upstream sent no headers within " + headerTimeout.String(),
		upstream: target,
	}
}

// relayFault converts a non-200 upstream response into this API's own
// fault, with the upstream's detail. A 400 and a 404 keep their status.
// A 409 becomes problemAway with no Retry-After, because the upstreams
// return 409 only for a Display with no monitor connected or a Sink or
// Source with no node, and a retry fails until a person fixes that. A
// 503 becomes problemCaptureBusy with the upstream's Retry-After. Any
// other status becomes a 502, and so does any response that is not a
// problem document, because this API cannot tell what its status
// means.
func relayFault(response *http.Response, target string) *upstreamFault {
	detail, problem := upstreamDetail(response)
	switch {
	case !problem:
		return &upstreamFault{
			status: http.StatusBadGateway, kind: problemUpstreamFailed,
			title:  "The upstream sent a response that this API cannot relay",
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
	case response.StatusCode == http.StatusConflict:
		return &upstreamFault{
			status: http.StatusConflict, kind: problemAway,
			title: "The upstream object is away", detail: detail,
			upstream: target,
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
			title:  "The upstream returned a status that this API cannot relay",
			detail: detail, upstream: target,
		}
	}
}

// upstreamDetail returns the text to show for an upstream response and
// whether the response was a problem document. For a problem document
// the text is its detail, or its title when the detail is empty. For
// any other response it is the status line and the body.
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

// upstreamCodecs returns the codecs parameter of an upstream's
// Content-Type, or an empty string when there is none. The composed
// response reports only the codecs that an upstream sent, never a guess.
func upstreamCodecs(contentType string) string {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parameters["codecs"])
}

// maxDrain is the most that drain reads, because past this size a new
// connection costs less than reading the rest of the body.
const maxDrain = 4 << 20

// drain reads the rest of a body, up to maxDrain, and closes it, because
// the HTTP transport reuses a connection only after its body was read
// to EOF. A body closed early costs a new connection and TLS handshake.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
