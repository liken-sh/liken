package main

// What every provider client shares: one request form, the 429 cooldown rule,
// and the error an answer outside 2xx becomes. Each provider file holds its
// own address, its own auth form, and the calls it makes.
// The pace between two requests is shared here too, so no client sends faster
// than its provider asks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The cooldown a 429 takes when no header names one.
const providerCooldown = 10 * time.Second

// The longest cooldown a container waits out. A provider that counts a daily
// allowance names a reset hours away once the day is spent, and a container
// that slept that long would hold its Job for the whole wait. A longer
// cooldown fails the request at once with the provider's own answer, and the
// fact asks again on a later run.
const providerLongestCooldown = time.Minute

// How many times one request goes out, so a provider that answers 429 without
// end fails the attempt instead of holding the container.
const providerAttempts = 3

// One request's bound, so a provider that stops answering cannot hold the
// container open.
var providerRequestTimeout = 30 * time.Second

// One answer's bound, so a provider that streams without end cannot grow the
// container.
const providerAnswerLimit = 1 << 20

// The interval one block takes, out of the table, or none for a block the
// table holds no row for. It is a variable so a test drives a pace of its own
// and no test sleeps.
var providerPaceFor = func(block string) time.Duration { return blockOf(block).pace }

// When the next request of one client may go. A pointer, so every copy of the
// client takes its slots from one line.
type providerSlot struct {
	mutex sync.Mutex
	next  time.Time
}

// What every client is made of: the block name, which names the provider in
// an error; the address, which only a test replaces; the wait a cooldown
// takes, which a test replaces so no test sleeps; and the form the key
// travels in.
// It holds the pace as well: the interval between two of its requests, which
// a test zeroes, and the slot the next one takes.
type providerRequests struct {
	provider  string
	base      string
	http      *http.Client
	wait      func(context.Context, time.Duration) error
	authorize func(*http.Request)
	interval  time.Duration
	slot      *providerSlot
	// The counts of this client's requests, recorded for the enricher that
	// built it. A caller with no enricher records none.
	tallies *tallies
}

// recordTo names the recorder every request of this client counts into. The
// operator's provider check builds no enricher and names none.
func (r *providerRequests) recordTo(record *tallies) {
	r.tallies = record
}

// count records one request under this provider and the class of its answer,
// with the seconds it took.
func (r *providerRequests) count(status string, took time.Duration) {
	r.tallies.add(tallyProviderRequests, 1, "provider", r.provider, "status", status)
	r.tallies.add(tallyProviderRequestSeconds, took.Seconds(), "provider", r.provider)
}

// providerStatusClass is the class one answer counts under. A request that
// never got an answer counts as an error, and so does a status outside these
// ranges.
func providerStatusClass(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "429"
	case status >= 200 && status <= 299:
		return "2xx"
	case status >= 400 && status <= 499:
		return "4xx"
	case status >= 500 && status <= 599:
		return "5xx"
	}
	return providerStatusErrorClass
}

// providerStatusErrorClass is the class a request with no answer counts under.
const providerStatusErrorClass = "error"

// roundTrip is the whole round trip every send shares: the answer's status,
// the cooldown its header names, and the body, which the caller reads as bytes
// or streams onto the volume. Every answer counts under this provider, so a
// send that reads the body either way is one request.
func (r *providerRequests) roundTrip(request *http.Request,
	take func(status int, body io.Reader) error) (int, time.Duration, error) {
	started := time.Now()
	response, err := r.http.Do(request)
	if err != nil {
		r.count(providerStatusErrorClass, time.Since(started))
		return 0, 0, err
	}
	defer drain(response.Body)

	takeErr := take(response.StatusCode, response.Body)
	r.count(providerStatusClass(response.StatusCode), time.Since(started))
	if takeErr != nil {
		return 0, 0, takeErr
	}
	return response.StatusCode, cooldownOf(response.Header), nil
}

// roundTripBytes reads the answer into memory, up to the caller's own bound.
// Both JSON sends and the file fetch read their answer this way.
func (r *providerRequests) roundTripBytes(request *http.Request, limit int64) (int, time.Duration, []byte, error) {
	var body []byte
	status, cooldown, err := r.roundTrip(request, func(_ int, from io.Reader) error {
		read, readErr := io.ReadAll(io.LimitReader(from, limit))
		body = read
		return readErr
	})
	if err != nil {
		return status, cooldown, nil, err
	}
	return status, cooldown, body, nil
}

// The requests one account makes. A provider that needs no key authorizes
// nothing.
func newProviderRequests(provider, base string, authorize func(*http.Request)) providerRequests {
	return providerRequests{
		provider:  provider,
		base:      base,
		http:      &http.Client{Timeout: providerRequestTimeout},
		wait:      waitFor,
		authorize: authorize,
		interval:  providerPaceFor(provider),
		slot:      &providerSlot{},
	}
}

// Each request takes its slot under the lock and waits for it outside, so two
// callers queue instead of waking together. The wait is this layer's own, not
// the replaceable one a cooldown takes, so a test that counts cooldowns
// counts no slots.
func (r *providerRequests) pace(ctx context.Context) error {
	if r.slot == nil {
		return nil
	}
	r.slot.mutex.Lock()
	now := time.Now()
	wait := r.slot.next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	r.slot.next = now.Add(wait + r.interval)
	r.slot.mutex.Unlock()
	if wait <= 0 {
		return nil
	}
	return waitFor(ctx, wait)
}

// The wait ends on the context as well as on the clock, so a container that
// is told to stop does not sleep out its cooldown first.
func waitFor(ctx context.Context, cooldown time.Duration) error {
	timer := time.NewTimer(cooldown)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// The answer a provider gave outside 2xx. It is a type and not a sentence
// alone, so a caller reads the status back with errors.As and tells a title
// the provider does not hold from a key it refused.
type providerStatusError struct {
	provider string
	path     string
	status   int
	body     string
}

func (e providerStatusError) Error() string {
	return fmt.Sprintf("%s %s: %d: %s", e.provider, e.path, e.status, e.body)
}

// Whether this error is the provider's answer with that status.
func answeredWith(err error, status int) bool {
	answer := providerStatusError{}
	return errors.As(err, &answer) && answer.status == status
}

// Whether a 429 is worth a wait and another request: the attempts are not
// spent, and the cooldown is one a container can wait out.
func waitable(status int, cooldown time.Duration, attempt int) bool {
	return status == http.StatusTooManyRequests && attempt < providerAttempts &&
		cooldown <= providerLongestCooldown
}

// The whole retry rule: a 429 waits the cooldown its headers name, or ten
// seconds where they name none, and the request goes out again.
func (r *providerRequests) get(ctx context.Context, path string, query url.Values, into any) error {
	for attempt := 1; ; attempt++ {
		status, cooldown, body, err := r.send(ctx, path, query)
		if err != nil {
			return err
		}
		if waitable(status, cooldown, attempt) {
			if err := r.wait(ctx, cooldown); err != nil {
				return err
			}
			continue
		}
		if status < 200 || status > 299 {
			return providerStatusError{provider: r.provider, path: path,
				status: status, body: strings.TrimSpace(string(body))}
		}
		return json.Unmarshal(body, into)
	}
}

// The send builds the request and lets the key's own shape decide the form it
// travels in.
func (r *providerRequests) send(ctx context.Context, path string, query url.Values) (int, time.Duration, []byte, error) {
	if err := r.pace(ctx); err != nil {
		return 0, 0, nil, err
	}
	address := r.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	request.Header.Set("Accept", jsonContentType)
	if r.authorize != nil {
		r.authorize(request)
	}
	return r.roundTripBytes(request, providerAnswerLimit)
}

// One file, by the retry rule the JSON calls follow. It carries no credential
// and asks for no JSON, because the host that serves a provider's images and
// headshots is a plain file host. Its bound is above the answer's, because a
// file is larger than an answer, and the caller holds the bytes only until
// the write door has them.
const providerFileLimit = 16 << 20

func (r *providerRequests) fetchFile(ctx context.Context, address string) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		status, cooldown, body, err := r.sendFile(ctx, address)
		if err != nil {
			return nil, err
		}
		if waitable(status, cooldown, attempt) {
			if err := r.wait(ctx, cooldown); err != nil {
				return nil, err
			}
			continue
		}
		if status < 200 || status > 299 {
			return nil, providerStatusError{provider: r.provider, path: address, status: status}
		}
		if len(body) == 0 {
			return nil, providerStatusError{provider: r.provider, path: address,
				status: status, body: "the answer was empty"}
		}
		return body, nil
	}
}

func (r *providerRequests) sendFile(ctx context.Context, address string) (int, time.Duration, []byte, error) {
	if err := r.pace(ctx); err != nil {
		return 0, 0, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	return r.roundTripBytes(request, providerFileLimit)
}

// fetchInto streams one file straight onto the volume, by the retry rule the
// other sends follow, because a video is larger than any answer this image
// holds in memory. The client's own request timeout bounds an answer and not
// a file, so the caller names the timeout the whole stream runs inside, and
// the bytes it may read.
func (r *providerRequests) fetchInto(ctx context.Context, address string, into io.Writer,
	timeout time.Duration, limit int64) (int64, error) {
	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The copy keeps the slot and the recorder of the client it was made
	// from, and gets an HTTP client of its own so no other request gets this
	// one's timeout.
	puller := *r
	client := *r.http
	client.Timeout = timeout
	puller.http = &client

	for attempt := 1; ; attempt++ {
		written, status, cooldown, err := puller.sendInto(timed, address, into, limit)
		if err != nil {
			return written, err
		}
		if waitable(status, cooldown, attempt) {
			if err := r.wait(timed, cooldown); err != nil {
				return 0, err
			}
			continue
		}
		if status < 200 || status > 299 {
			return 0, providerStatusError{provider: r.provider, path: address, status: status}
		}
		return written, nil
	}
}

// sendInto is the send that writes the answer out instead of holding it. The
// body reaches the writer on a 2xx alone, so a refusal and a cooldown leave
// the writer unchanged.
func (r *providerRequests) sendInto(ctx context.Context, address string, into io.Writer,
	limit int64) (int64, int, time.Duration, error) {
	if err := r.pace(ctx); err != nil {
		return 0, 0, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	if r.authorize != nil {
		r.authorize(request)
	}
	written := int64(0)
	status, cooldown, err := r.roundTrip(request, func(status int, body io.Reader) error {
		if status < 200 || status > 299 {
			return nil
		}
		copied, copyErr := io.Copy(into, io.LimitReader(body, limit+1))
		written = copied
		if copyErr != nil {
			return copyErr
		}
		if copied > limit {
			return fmt.Errorf("%s answered more than %d bytes for %s",
				r.provider, limit, address)
		}
		return nil
	})
	return written, status, cooldown, err
}

// The cooldown one answer's headers name. Retry-After is the standard
// header, and a provider that sends it means it. TheIntroDB names its two
// windows in headers of its own instead: X-RateLimit-* for its window of
// seconds, and X-UsageLimit-* for its daily allowance. Each window names the
// requests it has left and the seconds until it resets, and the window with
// none left is the one that refused. The daily window is read first, because
// its reset is the later one when both are spent.
func cooldownOf(header http.Header) time.Duration {
	if value := header.Get("Retry-After"); value != "" {
		return retryAfter(value)
	}
	for _, window := range []string{"X-UsageLimit", "X-RateLimit"} {
		if strings.TrimSpace(header.Get(window+"-Remaining")) == "0" {
			return retryAfter(header.Get(window + "-Reset"))
		}
	}
	return providerCooldown
}

// An unreadable or absent header takes the fixed cooldown.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return providerCooldown
	}
	return time.Duration(seconds) * time.Second
}

// A key that travels as a query parameter, which is the form OMDb and
// Fanart.tv take. The name is the parameter the provider reads it from.
func queryKey(name, key string) func(*http.Request) {
	return func(request *http.Request) {
		query := request.URL.Query()
		query.Set(name, key)
		request.URL.RawQuery = query.Encode()
	}
}
