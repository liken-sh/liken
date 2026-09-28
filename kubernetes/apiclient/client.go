// Package apiclient sends an operator's reads and writes to the
// Kubernetes API server.
//
// The Kubernetes API is HTTPS that serves JSON, and each read or write
// is one request, so this client uses only net/http and encoding/json.
// It imports nothing from k8s.io. A program that must not link
// client-go, such as a pod build of an operator or a command-line tool,
// can use it. The watches run on client-go's reflector, in the informer
// package, because upstream maintains and tests that loop.
//
// Every pod starts with what it needs to reach the API server.
// Kubernetes injects two environment variables that name the server's
// in-cluster address, and the kubelet mounts a CA certificate and a
// ServiceAccount token at a known path. Those five values are the whole
// of what client-go's rest.InClusterConfig() reads.
package apiclient

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
)

// ServiceAccountDir is the directory where the kubelet mounts each
// container's API credentials.
const ServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// ErrNotFound separates "this object does not exist" from a real
// failure. An absent object is a normal state, and the caller handles
// it by creating the object. ErrConflict separates "something else
// wrote this object first" in the same way. That is a normal state
// under optimistic concurrency, and the caller handles it by reading
// the object again.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict: something else wrote this object first")
)

// Stale reports whether a write failed because the copy it was made
// from is not the API server's copy: another writer changed the
// object, or deleted it.
func Stale(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound)
}

// Client sends requests to one API server.
type Client struct {
	base        string
	http        *http.Client
	credentials string

	// throttleUnit is one second of the wait that a 429 asks for. The
	// tests make it shorter, so a test of the retry waits milliseconds.
	throttleUnit time.Duration
}

// New builds a client from its three parts. InCluster reads them from
// the pod's environment, and a test takes them from an httptest
// server. An empty credentials directory sends no token.
func New(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{base: base, http: httpClient, credentials: credentials, throttleUnit: time.Second}
}

// InClusterOptions are the optional parts of an in-cluster client.
type InClusterOptions struct {
	// ServiceAccountDir is the directory that holds the CA and the
	// token. Empty means the directory the kubelet mounts.
	ServiceAccountDir string
}

// InCluster builds a client from the pod's environment and its
// ServiceAccount.
func InCluster(options InClusterOptions) (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST unset")
	}
	dir := options.ServiceAccountDir
	if dir == "" {
		dir = ServiceAccountDir
	}

	// The mounted CA is the cluster's own. The client trusts only that
	// CA, not the system trust store, so it accepts the cluster's API
	// server and refuses any other server that answers on the address.
	caPEM, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("service account CA contains no certificates")
	}

	return New("https://"+host+":"+port, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			// Each timeout limits the same failure: a server that stops
			// answering without sending anything. A machine that fails
			// sends no FIN and no RST, so a connection to it goes silent
			// and every wait on it would otherwise have no limit.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		// No request of this client streams, because the informer
		// package runs every watch. So the whole request, body
		// included, has a limit too, and a body that stops part way
		// cannot hold a pass.
		Timeout: 30 * time.Second,
	}, dir), nil
}

// RequestJSON sends one request with a JSON body, and decodes the JSON
// answer into out. A nil out discards the answer.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	return c.Request(method, path, "application/json", body, out)
}

// Request is RequestJSON with the body's content type stated. A PATCH
// needs it: the API server reads the patch's dialect from the header
// alone, and the same bytes mean different things as a merge patch, a
// JSON patch, and an apply.
//
// A 404 answers ErrNotFound, a 409 answers ErrConflict, and any other
// status that is not 2xx answers an error that holds the server's own
// message. A 429 is sent again after the wait the API server asks for,
// as maxThrottleWait describes.
func (c *Client) Request(method, path, contentType string, body []byte, out any) error {
	var waited time.Duration
	for {
		err := c.send(method, path, contentType, body, out)
		var throttled *throttledError
		if !errors.As(err, &throttled) || waited+throttled.wait > maxThrottleWait*c.throttleUnit {
			return err
		}
		time.Sleep(throttled.wait)
		waited += throttled.wait
	}
}

// maxThrottleWait is the longest total wait, in units of the wait a
// 429 asks for, before a request answers the 429 to its caller. While
// the API server starts the storage of a CRD it just received, it
// answers 429 with the reason "storage is (re)initializing" for a
// second or two. A rollout that changes a CRD meets that answer, and a
// pass that sent the request again after the wait gets the object. A
// server that answers 429 for longer is overloaded, and the caller's
// own retry, which waits longer, handles it.
//
// The wait does not end when the caller's work is cancelled, because
// the client takes no context. The limit bounds it instead: a request
// holds its caller for at most ten seconds of waits, plus the request
// timeout of each send. A caller that holds a lock across the request,
// such as memo.Versions.Send, holds it that long too, and a shutdown
// that waits on the caller waits that long.
const maxThrottleWait = 10

// send sends one request once.
func (c *Client) send(method, path, contentType string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	if err := c.authorize(req); err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		message := responseText(resp.Body)
		err := fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, message)
		if resp.StatusCode == http.StatusTooManyRequests {
			return &throttledError{err: err, wait: c.retryAfter(resp.Header.Get("Retry-After"), message)}
		}
		return err
	case out == nil:
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// authorize puts the ServiceAccount token on a request.
//
// The client reads its token from disk on every request. The tokens are
// short-lived, and the kubelet refreshes the mounted file as each one
// nears its expiry, so a client that holds a token in memory eventually
// gets 401 answers.
func (c *Client) authorize(req *http.Request) error {
	if c.credentials == "" {
		return nil
	}
	token, err := os.ReadFile(c.credentials + "/token")
	if err != nil {
		return fmt.Errorf("reading service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	return nil
}

// throttledError is a 429 from the API server, which asks the client to
// wait and send the request again.
type throttledError struct {
	err  error
	wait time.Duration
}

func (e *throttledError) Error() string { return e.err.Error() }

// retryAfter reads how long a 429 asks the client to wait: the
// Retry-After header, or the retryAfterSeconds of the Status body, or
// one second when the answer states neither.
func (c *Client) retryAfter(header, body string) time.Duration {
	var seconds int
	if _, err := fmt.Sscan(header, &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds) * c.throttleUnit
	}
	var status struct {
		Details struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(body), &status) == nil && status.Details.RetryAfterSeconds > 0 {
		return time.Duration(status.Details.RetryAfterSeconds) * c.throttleUnit
	}
	return c.throttleUnit
}

// responseText is the start of an answer's body, for an error that
// holds the server's own text. The API server ends its Status body with
// a newline, and the text leaves it out, so the error and the log line
// that prints it stay on one line.
func responseText(body io.Reader) string {
	message, _ := io.ReadAll(io.LimitReader(body, 2048))
	return strings.TrimRightFunc(string(message), unicode.IsSpace)
}

// maxDrain limits the read below. The largest answer an operator asks
// for is one list, which the caller decodes into memory anyway, so
// reading the tail costs nothing new. Past this size, the connection is
// the cheaper thing to lose.
const maxDrain = 4 << 20

// drain reads what the caller left in the response body, then closes
// it. Go returns a connection to its pool only when the body reaches
// EOF, so a body closed early costs a new TCP connection and TLS
// handshake on the next request. The early close also reaches the API
// server as a hang-up on a request it already answered. A 404, a 409,
// and a status write each answer with a body that no caller reads.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
