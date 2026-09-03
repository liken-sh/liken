package main

// What every provider client shares: one request form, the 429 cooldown rule,
// and the error an answer outside 2xx becomes. Each provider file holds its
// own address, its own auth form, and the calls it makes.

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
	"time"
)

// The cooldown a 429 with no Retry-After header takes.
const providerCooldown = 10 * time.Second

// How many times one request goes out, so a provider that answers 429 without
// end fails the attempt instead of holding the container.
const providerAttempts = 3

// One request's bound, so a provider that stops answering cannot hold the
// container open.
var providerRequestTimeout = 30 * time.Second

// One answer's bound, so a provider that streams without end cannot grow the
// container.
const providerAnswerLimit = 1 << 20

// What every client is made of: the block name, which names the provider in
// an error; the address, which only a test replaces; the wait a cooldown
// takes, which a test replaces so no test sleeps; and the form the key
// travels in.
type providerRequests struct {
	provider  string
	base      string
	http      *http.Client
	wait      func(context.Context, time.Duration) error
	authorize func(*http.Request)
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
	}
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

// The whole retry rule: a 429 waits the header's own cooldown, or ten seconds
// where it names none, and the request goes out again.
func (r *providerRequests) get(ctx context.Context, path string, query url.Values, into any) error {
	for attempt := 1; ; attempt++ {
		status, cooldown, body, err := r.send(ctx, path, query)
		if err != nil {
			return err
		}
		if status == http.StatusTooManyRequests && attempt < providerAttempts {
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

	response, err := r.http.Do(request)
	if err != nil {
		return 0, 0, nil, err
	}
	defer drain(response.Body)

	body, err := io.ReadAll(io.LimitReader(response.Body, providerAnswerLimit))
	if err != nil {
		return 0, 0, nil, err
	}
	return response.StatusCode, retryAfter(response.Header.Get("Retry-After")), body, nil
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
		if status == http.StatusTooManyRequests && attempt < providerAttempts {
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	response, err := r.http.Do(request)
	if err != nil {
		return 0, 0, nil, err
	}
	defer drain(response.Body)

	body, err := io.ReadAll(io.LimitReader(response.Body, providerFileLimit))
	if err != nil {
		return 0, 0, nil, err
	}
	return response.StatusCode, retryAfter(response.Header.Get("Retry-After")), body, nil
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
