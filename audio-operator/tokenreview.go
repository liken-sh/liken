package main

// Both HTTP servers of this component authenticate a bearer token with
// a TokenReview. audio-api reviews a caller's token for the audience
// audio-api. The capture container reviews the projected token that
// audio-api sends with each request it forwards, for the audience
// audio-capture. The API server checks the token with its own keys, so
// neither server has a shared Secret or a key of its own to rotate.

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

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// A pod's default ServiceAccount token has only the API server's
// audience, so it fails review for both of these. A client must mount a
// projected token requested for one of them.
const (
	apiAudience     = "audio-api"
	captureAudience = "audio-capture"
)

// captureCaller is the only username that the capture container
// accepts. It is the ServiceAccount of audio-api, which forwards
// requests to the capture container.
const captureCaller = "system:serviceaccount:liken-system:audio-api"

const tokenReviewPath = "/apis/authentication.k8s.io/v1/tokenreviews"

// tokenCacheMax is the longest time that a reviewer accepts a token
// from its cache without a new TokenReview. The cache entry expires
// sooner when the token's exp claim comes sooner. When the API server
// starts to refuse a token before its exp claim, the reviewer can still
// accept it from the cache for up to tokenCacheMax.
const tokenCacheMax = 60 * time.Second

// When the cache has cacheSweepSize entries or more, store deletes the
// expired ones before it adds an entry. cached deletes an expired entry
// only when a client presents its token again, and a client that gets a
// new token for each request never does. Without the sweep, the map
// grows for as long as the process runs.
const cacheSweepSize = 64

// ErrTokenDenied is the error that review wraps when the API server
// refuses the token: the TokenReview returned authenticated false, or
// its status.audiences lacks the reviewer's audience. audio-api responds
// to this error with a 401, and to any other error of review, such as a
// failed request to the API server, with a 503, because the API server
// did not judge the token. The capture container responds to every
// error of review with a 401.
var ErrTokenDenied = errors.New("the token review refused the token")

// caller is the authenticated identity of a request. A TokenReview
// fills every field, and a verified client certificate fills only
// Username and Groups. The routes and the SubjectAccessReview read
// caller, so they do not depend on which credential the client used.
type caller struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string][]string
}

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

// reviewer sends TokenReviews for one audience and caches the identity
// of each token that passes. It never caches a refusal.
type reviewer struct {
	client   *apiclient.Client
	audience string

	// A test replaces now to move the cache past an expiry without
	// waiting for it.
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
	calls   int
}

type cacheEntry struct {
	who   caller
	until time.Time
}

func newReviewer(client *apiclient.Client, audience string) *reviewer {
	return &reviewer{
		client:   client,
		audience: audience,
		now:      time.Now,
		entries:  map[string]cacheEntry{},
	}
}

// review returns the identity of the token's owner, from the cache when
// it has the token. When the API server refuses the token, the error
// wraps ErrTokenDenied, and its text has the TokenReview's status.error
// or the audience that the token lacks. Both servers send that text to
// the client as the error_description of their 401.
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
	var response tokenReview
	if err := r.client.RequestJSON("POST", tokenReviewPath, body, &response); err != nil {
		return caller{}, fmt.Errorf("reviewing the token: %w", err)
	}
	if !response.Status.Authenticated {
		return caller{}, fmt.Errorf("%w: %s", ErrTokenDenied, tokenRefusalReason(response.Status.Error))
	}
	// status.authenticated alone does not prove the audience. An
	// authenticator that does not support audiences ignores
	// spec.audiences and can return authenticated with an empty
	// status.audiences, which means that the token is for the API server
	// itself. The TokenReview API documentation tells a client that sets
	// spec.audiences to check that status.audiences has one of them.
	if !slices.Contains(response.Status.Audiences, r.audience) {
		return caller{}, fmt.Errorf("%w: the token is not for the audience %s", ErrTokenDenied, r.audience)
	}

	who := caller{
		Username: response.Status.User.Username,
		UID:      response.Status.User.UID,
		Groups:   response.Status.User.Groups,
		Extra:    response.Status.User.Extra,
	}
	r.store(key, who, token)
	return who, nil
}

// tokenRefusalReason returns status.error, or a fixed reason when the API
// server left it empty, so the error_description of a 401 is never
// empty.
func tokenRefusalReason(text string) string {
	if strings.TrimSpace(text) == "" {
		return "the API server reported the token as not authenticated and gave no reason"
	}
	return text
}

func (r *reviewer) cached(key string) (caller, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, found := r.entries[key]
	if !found {
		return caller{}, false
	}
	if !r.now().Before(entry.until) {
		delete(r.entries, key)
		return caller{}, false
	}
	return entry.who, true
}

// store caches the identity for tokenCacheMax, or until the token's exp
// claim when that comes sooner, so the cache never accepts a token after
// its exp claim.
func (r *reviewer) store(key string, who caller, token string) {
	until := r.now().Add(tokenCacheMax)
	if expiry, found := tokenExpiry(token); found && expiry.Before(until) {
		until = expiry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= cacheSweepSize {
		now := r.now()
		for cachedKey, entry := range r.entries {
			if !now.Before(entry.until) {
				delete(r.entries, cachedKey)
			}
		}
	}
	r.entries[key] = cacheEntry{who: who, until: until}
}

func (r *reviewer) counted() {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
}

func (r *reviewer) reviews() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// tokenKey returns the hex SHA-256 of the token. The cache uses it as
// the key, so the map holds no credential that a client could present.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// tokenExpiry reads the exp claim from the payload of a ServiceAccount
// JWT without checking the signature. That is enough, because review
// calls it only after the API server accepted the token, and the value
// can only shorten how long the cache keeps the identity.
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

// bearerToken returns the credential of an Authorization header whose
// scheme is Bearer, in any letter case, because HTTP compares
// authentication schemes without case. It returns false for a missing
// header, another scheme, or an empty credential. Both servers respond
// to false with a 401 whose challenge has only the realm, because RFC
// 6750 section 3.1 gives no error code to a request that has no
// authentication.
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
