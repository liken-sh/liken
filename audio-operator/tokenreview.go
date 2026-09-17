package main

// Asking the API server who is calling.
//
// A TokenReview is the Kubernetes mechanism for "who is calling me".
// It uses the API server's own keys, and it needs no secret to mount
// and no rotation of its own, which is why there is no minted shared
// secret here. Both processes use it. The API reviews the caller's
// token with the audience audio-api; the capture container reviews
// the API's own projected token with the audience audio-capture.
//
// The review carries the audience in its spec and the answer carries
// status.audiences. This code reads the answer rather than trusting
// the ask, so a token minted for another audience never reaches a
// route here.
//
// The cache key is the SHA-256 of the raw token. A positive verdict
// is kept for the lesser of the token's own expiry and sixty seconds,
// a denial is never cached, and the key is never logged.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The two audiences. An audience is what keeps every pod's ordinary
// API-server token out of these two doors: a token minted for the API
// server alone reviews as not for either one.
const (
	apiAudience     = "audio-api"
	captureAudience = "audio-capture"
)

// captureCaller is the one identity the capture container accepts on
// its private leg.
const captureCaller = "system:serviceaccount:liken-system:audio-api"

// tokenReviewPath is where a TokenReview is created.
const tokenReviewPath = "/apis/authentication.k8s.io/v1/tokenreviews"

// tokenCacheMax bounds how long a positive verdict is kept.
const tokenCacheMax = 60 * time.Second

// sweepAbove is how many verdicts the cache holds before a new one
// clears the expired ones out. Every verdict is kept at most a minute,
// so the sweep is what bounds the map rather than what keeps it small.
const sweepAbove = 64

// ErrTokenDenied marks a token the API server refused, which is a 401
// with the review's own words. Every other failure of a review is the
// API server not answering, which is a 503.
var ErrTokenDenied = errors.New("the token review refused the token")

// caller is who is calling. A TokenReview answers with all four
// fields, and a verified client certificate answers with the user and
// the groups alone, so every route and every record reads one type
// and never checks which credential arrived. A SubjectAccessReview
// copies all four.
type caller struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string][]string
}

// tokenReview is the object sent and the object returned.
type tokenReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Spec       tokenReviewSpec   `json:"spec"`
	Status     tokenReviewStatus `json:"status,omitempty"`
}

type tokenReviewSpec struct {
	Token     string   `json:"token"`
	Audiences []string `json:"audiences,omitempty"`
}

type tokenReviewStatus struct {
	Authenticated bool          `json:"authenticated"`
	User          tokenUserInfo `json:"user"`
	Audiences     []string      `json:"audiences,omitempty"`
	Error         string        `json:"error,omitempty"`
}

type tokenUserInfo struct {
	Username string              `json:"username"`
	UID      string              `json:"uid"`
	Groups   []string            `json:"groups"`
	Extra    map[string][]string `json:"extra"`
}

// reviewer reviews tokens for one audience and keeps the positive
// verdicts.
type reviewer struct {
	client   *Client
	audience string

	// now is a field so a test reads a fixed clock off the cache
	// instead of waiting for a real minute to pass.
	now func() time.Time

	mu    sync.Mutex
	kept  map[string]keptVerdict
	calls int
}

type keptVerdict struct {
	who   caller
	until time.Time
}

func newReviewer(client *Client, audience string) *reviewer {
	return &reviewer{
		client:   client,
		audience: audience,
		now:      time.Now,
		kept:     map[string]keptVerdict{},
	}
}

// review answers who holds the token, or why the API server refused
// it. A refusal carries the review's own words, which is what the
// 401's error_description publishes.
func (r *reviewer) review(token string) (caller, error) {
	key := tokenKey(token)
	if who, found := r.cached(key); found {
		return who, nil
	}

	sent := tokenReview{
		APIVersion: "authentication.k8s.io/v1",
		Kind:       "TokenReview",
		Spec:       tokenReviewSpec{Token: token, Audiences: []string{r.audience}},
	}
	body, err := json.Marshal(&sent)
	if err != nil {
		return caller{}, err
	}
	r.counted()
	var answer tokenReview
	if err := r.client.RequestJSON("POST", tokenReviewPath, body, &answer); err != nil {
		return caller{}, fmt.Errorf("reviewing the token: %w", err)
	}
	if !answer.Status.Authenticated {
		return caller{}, fmt.Errorf("%w: %s", ErrTokenDenied, refusalWords(answer.Status.Error))
	}
	// The ask is not the answer. A token minted for another audience
	// comes back authenticated with its own audiences, so the answer
	// is what decides.
	if !slices.Contains(answer.Status.Audiences, r.audience) {
		return caller{}, fmt.Errorf("%w: the token is not for the audience %s", ErrTokenDenied, r.audience)
	}

	who := caller{
		Username: answer.Status.User.Username,
		UID:      answer.Status.User.UID,
		Groups:   answer.Status.User.Groups,
		Extra:    answer.Status.User.Extra,
	}
	r.keep(key, who, token)
	return who, nil
}

// refusalWords is the review's own text, or a stand-in when the API
// server sent none. A 401 has to say something, and "authenticated is
// false with no error" is what it says then.
func refusalWords(text string) string {
	if strings.TrimSpace(text) == "" {
		return "the API server reported the token as not authenticated and gave no reason"
	}
	return text
}

func (r *reviewer) cached(key string) (caller, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	verdict, found := r.kept[key]
	if !found {
		return caller{}, false
	}
	if !r.now().Before(verdict.until) {
		delete(r.kept, key)
		return caller{}, false
	}
	return verdict.who, true
}

// keep holds a positive verdict for the lesser of the token's own
// expiry and tokenCacheMax, so a token the API server would refuse a
// moment from now is never accepted from memory.
//
// The sweep is here because an expired verdict otherwise leaves the
// map only when the same token is looked up again. A caller that
// presents a fresh token every time never looks any of them up twice,
// so without the sweep the map would grow for as long as the process
// runs.
func (r *reviewer) keep(key string, who caller, token string) {
	until := r.now().Add(tokenCacheMax)
	if expiry, found := tokenExpiry(token); found && expiry.Before(until) {
		until = expiry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.kept) >= sweepAbove {
		now := r.now()
		for held, verdict := range r.kept {
			if !now.Before(verdict.until) {
				delete(r.kept, held)
			}
		}
	}
	r.kept[key] = keptVerdict{who: who, until: until}
}

func (r *reviewer) counted() {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
}

// reviews is how many times this reviewer has asked the API server,
// which is what a test reads to prove the cache holds.
func (r *reviewer) reviews() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// tokenKey is the cache key: the SHA-256 of the raw token. The token
// itself never enters the map, and the key never enters a log line.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenExpiry reads the exp claim of a ServiceAccount token. The
// signature is not checked here, because the TokenReview above already
// checked it; this read only shortens how long the verdict is kept.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Expiry int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expiry == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Expiry, 0), true
}

// bearerToken reads the credential out of an Authorization header. An
// absent header and a header of another scheme both answer with no
// token, which is the 401 that carries the realm alone (RFC 6750
// section 2.1).
func bearerToken(header string) (string, bool) {
	scheme, credential, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", false
	}
	return credential, true
}
