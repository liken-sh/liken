package main

// The display API forwards a capture from the capture sidecar on a
// node to its caller. The request to the sidecar uses HTTPS, because it
// sends a ServiceAccount bearer token and the response is an image of
// the screen, and the pod network does not encrypt either. A pod IP
// cannot match a certificate, so the client verifies every sidecar's
// certificate against sidecarName, the name that the API signs into
// it, whatever address the client dials.

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

// sidecarPort is the port the sidecar serves captures and metrics on.
// The operator container in the same pod listens on 9200, which every
// `liken` process uses for metrics, so the sidecar uses 9201.
const sidecarPort = 9201

// captureTokenPath is the projected ServiceAccount token that the
// `Deployment` mounts. Its audience is `display-capture`, the
// audience that the sidecar accepts.
const captureTokenPath = "/var/run/secrets/display-capture/token"

// problemReadLimit is how much of a sidecar's response body the API
// reads to decode a problem document or a screenInfo, so an unexpected
// large body is not read into memory. A problem document is a few
// hundred bytes.
const problemReadLimit = 8 << 10

type sidecarClient struct {
	http      *http.Client
	tokenPath string
	// port is a field so that a test can reach a sidecar that
	// listens on a port the kernel chose.
	port int
}

func newSidecarClient(anchor *x509.CertPool, tokenPath string) *sidecarClient {
	return &sidecarClient{
		port: sidecarPort,
		http: &http.Client{
			Transport: &http.Transport{
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

// request builds a GET to the sidecar on pod. It reads the token file
// on every request, because the kubelet rewrites the projected token
// before it expires.
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

// open sends a request to the sidecar and returns the response of a
// 200, with the function that cancels the request, which the caller
// calls when it has read the body. Any other outcome returns a fault:
// a 503 when the token cannot be read, a 504 when no headers arrive
// within deadline, the sidecar's own problem document, a 502 for any
// other response, or what dialFault returns for a request that got no
// response.
func (c *sidecarClient) open(ctx context.Context, pod sidecarPod, path, query string,
	deadline time.Duration) (*http.Response, context.CancelFunc, *fault) {
	bounded, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(deadline, cancel)
	req, err := c.request(bounded, pod, path, query)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, nil, unreachable(pod, err, "cannot be called")
	}
	resp, err := c.http.Do(req)
	stopped := timer.Stop()
	if err != nil {
		cancel()
		if !stopped {
			late := gatewayTimeout(fmt.Sprintf(
				"the capture sidecar on %s sent no headers within %s", pod.Node, deadline))
			late.log = fmt.Sprintf("%s/%s at %s: %v", pod.Namespace, pod.Name, pod.IP, err)
			return nil, nil, late
		}
		return nil, nil, dialFault(pod, err)
	}
	if resp.StatusCode == http.StatusOK {
		// The caller reads the body after open returns, so the
		// request's context must stay live until the caller cancels it.
		return resp, cancel, nil
	}
	relay := relayed(resp)
	drain(resp.Body)
	cancel()
	return nil, nil, relay
}

// dialFault turns the error of a request that got no response into a
// fault. A refused, failed, or unreachable dial is a 503, because a
// retry may clear it. A certificate that does not verify is a 503 too,
// because the sidecar serves a self-signed certificate until the
// `Secret` that the API writes reaches its volume, and the API writes
// that `Secret` again whenever it is missing. Any other error is a 502.
// A cancelled request also gets a fault here, and serveCapture checks
// for a caller that hung up before it writes the fault.
//
// The caller's message has the node and the kind of failure. The pod
// address goes to the log only, because a caller who may read screens
// has no need for the cluster's addresses.
func dialFault(pod sidecarPod, err error) *fault {
	refused := unreachable(pod, err, "refused the connection")
	if errors.Is(err, syscall.ECONNREFUSED) {
		return refused
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return unreachable(pod, err, "is not reachable from this API")
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return unreachable(pod, err, "did not present a certificate this API trusts")
	}
	var operation *net.OpError
	if errors.As(err, &operation) && operation.Op == "dial" {
		return refused
	}
	broken := upstreamFailed(fmt.Sprintf("the capture sidecar on %s responded with something that is not HTTP", pod.Node))
	broken.log = fmt.Sprintf("%s/%s at %s: %v", pod.Namespace, pod.Name, pod.IP, err)
	return broken
}

func unreachable(pod sidecarPod, err error, because string) *fault {
	f := unavailable(problemUpstreamFailed,
		fmt.Sprintf("the capture sidecar on %s %s", pod.Node, because))
	f.log = fmt.Sprintf("%s/%s at %s: %v", pod.Namespace, pod.Name, pod.IP, err)
	return f
}

// relayed returns a fault that repeats the sidecar's problem document
// and its Retry-After header, so the caller gets the refusal that
// Weston or the sidecar made. A response that is not a problem
// document becomes a 502.
func relayed(resp *http.Response) *fault {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, problemReadLimit))
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), problemMediaType) {
		return upstreamFailed(fmt.Sprintf("the capture sidecar responded %s: %s",
			resp.Status, strings.TrimSpace(string(body))))
	}
	var document problemDocument
	if err := json.Unmarshal(body, &document); err != nil || document.Status == 0 {
		return upstreamFailed(fmt.Sprintf("the capture sidecar responded %s: %s",
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

// info asks the sidecar for the screen's values as the compositor
// reports them on its `wl_output`.
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

// capturePrivatePath returns the path of a capture on the sidecar. It
// has the same form as the public path, with the connector name where
// the public path has the `Display` name, because the compositor
// names outputs by connector.
func capturePrivatePath(connector, ext string) string {
	path := apiRoot + "/" + displaysPlural + "/" + connector
	if ext == "" {
		return path
	}
	return path + "/" + screenAspect + "." + ext
}

// serveCapture serves one capture request. A HEAD does not call the
// sidecar. A GET sends the response headers after the sidecar returns
// 200, copies the bytes as they arrive, and records a `Captured`
// `Event` when it has written any byte.
func (s *apiServer) serveCapture(w http.ResponseWriter, r *http.Request, route apiRoute,
	name string, screen *Display, mediaType string, chosen captureSelection, who *caller,
	id string, start time.Time, head bool) {
	at := s.now()
	if head {
		s.captureHeaders(w, route, name, mediaType, at)
		w.Header().Set("Content-Type", s.capturedAs(screen, mediaType, chosen))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		s.readings.recordRequest(route.template, r.Method, http.StatusOK, s.now().Sub(start))
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
	defer cancel()
	defer func() { _ = resp.Body.Close() }()

	// The sidecar's Content-Type has the parameters of the stream it is
	// encoding. A clip's codecs parameter then names the level the
	// encoder chose, not a level that the API worked out separately.
	served := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(served, mediaType) {
		served = s.capturedAs(screen, mediaType, chosen)
	}
	s.captureHeaders(w, route, name, mediaType, at)
	s.readings.recordRequest(route.template, r.Method, http.StatusOK, s.now().Sub(start))
	s.readings.streaming(route.aspect, 1)
	defer s.readings.streaming(route.aspect, -1)

	written, truncated := s.stream(w, r, served, resp.Body, cancel, seconds(chosen.Time.Begin))
	if written > 0 && s.record != nil {
		s.record(screen, who.Username, route.aspect, mediaType)
	}
	s.logged(r, route, id, truncatedDetail(truncated), http.StatusOK, written, start)
	if truncated && r.Context().Err() == nil {
		panic(http.ErrAbortHandler)
	}
}

// truncatedDetail returns the log detail for a capture whose body the
// sidecar ended early. The log line still has status 200, because the
// headers had been sent, so only the detail shows that the capture is
// incomplete.
func truncatedDetail(truncated bool) string {
	if truncated {
		return "the capture sidecar ended the body part way"
	}
	return ""
}

// stream writes the headers, then copies the sidecar's body to the
// caller. It returns the number of bytes written and whether the body
// ended with an error other than EOF. `nosniff` tells a browser to keep
// the Content-Type, because the bytes are an image or a video and never
// a page.
//
// The flush after the headers sends them at once. Go otherwise holds
// the headers until the body has bytes, and the first byte of a
// capture waits for the `t=` begin time. media-api aligns the tracks
// of a composed stream by the time each track's headers arrive, so
// headers held back would shift this track by the begin time.
//
// The copy flushes after every block, so a browser or mpv plays the
// capture as it arrives and the API holds one block in memory. The idle
// timer calls quiet to end a capture that the sidecar stops writing.
// Its first deadline is the begin time plus idleDeadline, so it does
// not fire while the sidecar discards the part before `t=`, and every
// block resets it to idleDeadline.
func (s *apiServer) stream(w http.ResponseWriter, r *http.Request, served string, body io.Reader,
	quiet context.CancelFunc, begins time.Duration) (int, bool) {
	w.Header().Set("Content-Type", served)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	control := http.NewResponseController(w)
	_ = control.Flush()

	idle := time.AfterFunc(begins+idleDeadline, quiet)
	defer idle.Stop()

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
				return written, false
			}
		}
		if err != nil {
			// A body that ends without the final chunk means the sidecar
			// failed after it sent its status line. serveCapture then
			// aborts this response, so the caller does not take it for
			// a complete capture.
			return written, err != io.EOF
		}
		if r.Context().Err() != nil {
			return written, false
		}
	}
}

// capturedAs returns the Content-Type that a capture will have,
// computed from the `Display`'s mode. A HEAD reports it because it
// calls no sidecar. A GET uses it when the sidecar's Content-Type does
// not start with the requested media type.
func (s *apiServer) capturedAs(screen *Display, mediaType string, chosen captureSelection) string {
	width, height, _ := screenMode(screen)
	switch {
	case chosen.Width > 0 && width > 0:
		height, width = height*chosen.Width/width, chosen.Width
	case chosen.Height > 0 && height > 0:
		width, height = width*chosen.Height/height, chosen.Height
	case width > clipWidthLimit:
		height, width = height*clipWidthLimit/width, clipWidthLimit
	}
	return captureContentType(mediaType, width, height, chosen.Framerate)
}

// maxDrain limits how much drain reads. The bodies that callers leave
// unread are small documents, and a body larger than this costs less
// to abandon with its connection than to read.
const maxDrain = 4 << 20

// drain reads what the caller left of a sidecar's response body, then
// closes it. Go returns a connection to its pool only when the body
// reaches EOF, so a body closed early costs a new TCP connection and
// TLS handshake on the next request to the same sidecar.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
