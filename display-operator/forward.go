package main

// This file is the private leg: display-api's own client to the
// capture sidecar on a node. The leg is HTTPS because the bearer
// token on it is a ServiceAccount token and the body is a picture of
// a room, and a pod network carries both in the clear otherwise. A
// pod IP is not a name, so the client verifies the sidecar's leaf
// under the one SAN the API minted it with, whatever address it
// dialed. The API copies bytes from the sidecar to the caller with a
// flush after every block and never buffers a response, so a clip
// plays as it arrives and a stream holds no more memory than one
// block.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The port the sidecar serves captures and metrics on. The operator
// container in the same pod holds 9200, the port every liken process
// serves metrics on, so the sidecar's one listener is on 9201.
const sidecarPort = 9201

// The projected ServiceAccount token the Deployment mounts, with the
// audience display-capture, the sidecar's own.
const captureTokenPath = "/var/run/secrets/display-capture/token"

// How much of a sidecar's answer the API reads before it decides the
// answer is not a problem document. A problem document is a few
// hundred bytes, and this bound keeps a wrong answer from being read
// whole.
const problemReadLimit = 8 << 10

// The client: one transport, one trust anchor, and the one name every
// sidecar's leaf is verified under.
type sidecarClient struct {
	http      *http.Client
	tokenPath string
	// The port is a field so a test reaches a sidecar that answers
	// on a port the kernel chose.
	port int
}

func newSidecarClient(anchor *x509.CertPool, tokenPath string) *sidecarClient {
	return &sidecarClient{
		port: sidecarPort,
		http: &http.Client{
			Transport: &http.Transport{
				// A pod address is not a name, so the handshake is
				// verified under the SAN the API minted the sidecar's
				// leaf with.
				TLSClientConfig: &tls.Config{RootCAs: anchor, ServerName: sidecarName, MinVersion: tls.VersionTLS12},
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 10 * time.Second,
				}).DialContext,
				IdleConnTimeout: 30 * time.Second,
			},
		},
		tokenPath: tokenPath,
	}
}

// The token is read from the file on every request, because the
// kubelet rewrites the projected token as it nears expiry.
func (c *sidecarClient) request(ctx context.Context, pod sidecarPod, path, query string) (*http.Request, error) {
	address := fmt.Sprintf("https://%s%s", net.JoinHostPort(pod.IP, strconv.Itoa(c.port)), path)
	if query != "" {
		address += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	token, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return nil, fmt.Errorf("reading the capture token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	return req, nil
}

// A sidecar's answer becomes one of four things. A 200 is a body the
// caller streams. A refused connection is 503, because a retry may
// clear it. No headers within the deadline is 504. A problem
// document is relayed whole, and anything else on the wire is 502.
func (c *sidecarClient) open(ctx context.Context, pod sidecarPod, path, query string,
	deadline time.Duration) (*http.Response, context.CancelFunc, *fault) {
	bounded, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(deadline, cancel)
	req, err := c.request(bounded, pod, path, query)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, nil, unavailable(problemUpstreamFailed, err.Error())
	}
	resp, err := c.http.Do(req)
	stopped := timer.Stop()
	if err != nil {
		cancel()
		if !stopped {
			return nil, nil, gatewayTimeout(fmt.Sprintf(
				"the capture sidecar %s/%s sent no headers within %s", pod.Namespace, pod.Name, deadline))
		}
		return nil, nil, dialFault(err)
	}
	if resp.StatusCode == http.StatusOK {
		// The body outlives this call and the caller ends it. Closing
		// the body stops the reading, and this cancel stops the request
		// that produces it.
		return resp, cancel, nil
	}
	// The problem document is read before the body is drained,
	// because the sidecar's own words are the whole of what a refusal
	// carries.
	relay := relayed(resp)
	drain(resp.Body)
	cancel()
	return nil, nil, relay
}

// A request to a node fails before an answer in two ways. A sidecar
// that refused the connection or is not there is a 503 a retry may
// clear. Anything else on the wire is a 502, because the sidecar
// answered and what it said was not HTTP. A cancelled request is
// neither: the caller hung up, and the caller is what the API checks
// before it answers at all.
func dialFault(err error) *fault {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return unavailable(problemUpstreamFailed, err.Error())
	}
	var operation *net.OpError
	if errors.As(err, &operation) && operation.Op == "dial" {
		return unavailable(problemUpstreamFailed, err.Error())
	}
	return upstreamFailed(err.Error())
}

// A problem document the sidecar sent is answered with its own
// status, its own type, and its own words, so weston's refusal
// reaches the caller whole, Retry-After included.
func relayed(resp *http.Response) *fault {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, problemReadLimit))
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), problemMediaType) {
		return upstreamFailed(fmt.Sprintf("the capture sidecar answered %s: %s",
			resp.Status, strings.TrimSpace(string(body))))
	}
	var document problemDocument
	if err := json.Unmarshal(body, &document); err != nil || document.Status == 0 {
		return upstreamFailed(fmt.Sprintf("the capture sidecar answered %s: %s",
			resp.Status, strings.TrimSpace(string(body))))
	}
	relay := &fault{
		status:     document.Status,
		kind:       document.Type,
		title:      document.Title,
		detail:     document.Detail,
		acceptable: document.Acceptable,
	}
	if after := resp.Header.Get("Retry-After"); after != "" {
		relay.headers = [][2]string{{"Retry-After", after}}
	}
	return relay
}

// The info route of the private leg answers the screen's own numbers
// as the compositor reports them on wl_output.
func (c *sidecarClient) info(ctx context.Context, pod sidecarPod, connector string) (screenInfo, *fault) {
	resp, cancel, f := c.open(ctx, pod, capturePrivatePath(connector, ""), "", headerDeadline)
	if f != nil {
		return screenInfo{}, f
	}
	defer cancel()
	defer drain(resp.Body)
	var info screenInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, problemReadLimit)).Decode(&info); err != nil {
		return screenInfo{}, upstreamFailed(err.Error())
	}
	return info, nil
}

// The private leg uses the same path grammar the public one does,
// with the connector name in the place of the Display's name,
// because the compositor names outputs by connector.
func capturePrivatePath(connector, ext string) string {
	path := apiRoot + "/" + displaysPlural + "/" + connector
	if ext == "" {
		return path
	}
	return path + "/" + screenAspect + "." + ext
}

// The whole of a capture on the public leg. A HEAD takes no frame
// and makes no call to the sidecar. A GET opens the private leg,
// sends the headers once the sidecar has answered 200, copies the
// bytes as they arrive, and writes the Captured Event once any byte
// has flowed.
func (s *apiServer) serveCapture(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, screen *Display, mediaType string, chosen captureSelection, who *reviewedToken,
	id string, start time.Time, head bool) {
	at := s.now()
	if head {
		s.captureHeaders(w, route, name, mediaType, at)
		w.WriteHeader(http.StatusOK)
		s.readings.answered(route.template, r.Method, http.StatusOK, s.now().Sub(start))
		s.logged(r, route, id, "", http.StatusOK, 0, start)
		return
	}

	pod, f := s.reach(screen)
	if f != nil {
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	connector := screen.Status.Connector
	if connector == "" {
		connector = name
	}
	deadline := headerDeadline + time.Duration(chosen.Time.Begin*float64(time.Second))

	resp, cancel, f := s.sidecar.open(r.Context(), pod,
		capturePrivatePath(connector, formExtension(mediaType)), r.URL.RawQuery, deadline)
	if f != nil {
		if s.hungUp(r, route, id, start) {
			return
		}
		s.refuse(w, r, route, id, start, head, f)
		return
	}
	// The request to the node ends with this response, whether the
	// caller read every byte or hung up.
	defer cancel()
	defer func() { _ = resp.Body.Close() }()

	s.captureHeaders(w, route, name, mediaType, at)
	w.WriteHeader(http.StatusOK)
	s.readings.answered(route.template, r.Method, http.StatusOK, s.now().Sub(start))
	s.readings.streaming(route.aspect, 1)
	defer s.readings.streaming(route.aspect, -1)

	written := s.stream(w, r, resp.Body, cancel, seconds(chosen.Time.Begin))
	if written > 0 && s.record != nil {
		s.record(name, who.Username, route.aspect, mediaType)
	}
	s.logged(r, route, id, "", http.StatusOK, written, start)
}

// The copy flushes after every block, so a browser and mpv both see
// the stream as it arrives. An idle timer ends a capture nothing is
// writing to. It is armed for the t= begin plus the idle deadline
// and reset by every block, so it never fires during the beginning
// the caller asked to discard, and it counts idle from the first
// body byte after that.
func (s *apiServer) stream(w http.ResponseWriter, r *http.Request, body io.Reader,
	quiet context.CancelFunc, begins time.Duration) int {
	idle := time.AfterFunc(begins+idleDeadline, quiet)
	defer idle.Stop()
	control := http.NewResponseController(w)

	buffer := make([]byte, 64<<10)
	written := 0
	for {
		count, err := body.Read(buffer)
		if count > 0 {
			idle.Reset(idleDeadline)
			sent, writeErr := w.Write(buffer[:count])
			written += sent
			_ = control.Flush()
			if writeErr != nil {
				return written
			}
		}
		if err != nil {
			return written
		}
		if r.Context().Err() != nil {
			return written
		}
	}
}
