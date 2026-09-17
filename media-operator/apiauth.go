package main

// This file holds the two questions the API asks the API server about
// every request: a TokenReview, which says who sent the token, and a
// SubjectAccessReview, which says whether that subject may read the
// aspect it asked for. The API server answers both rather than this
// process reading the token itself, because the API server holds the
// signing keys, the ServiceAccount state, and the RBAC rules, and a
// verdict from anywhere else would drift from them. The TokenReview
// names this API's audience, so a pod's default API-server token,
// minted for another audience, does not open it; only a token minted
// for `media-api` does.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// apiAudience is the audience every accepted token carries. A token
// without it, such as a pod's default API-server token, is refused
// with 401 even when the API server authenticates it.
const apiAudience = "media-api"

// The two review paths. Each review is a create: the API server
// answers the object it was sent, with its status filled in.
const (
	tokenReviewsPath         = "/apis/authentication.k8s.io/v1/tokenreviews"
	subjectAccessReviewsPath = "/apis/authorization.k8s.io/v1/subjectaccessreviews"
)

// verdictWindow is the longest a positive verdict is kept. It is
// short so a revoked token or a removed RoleBinding stops working
// within a minute, while a client that captures every few seconds
// costs the API server two reviews a minute rather than two a request.
const verdictWindow = 60 * time.Second

// captureSubject is who the request named, from a TokenReview or from
// a verified client certificate. The access review copies all four
// public fields, because RBAC binds to groups and to extra attributes
// as well as to a user name. A certificate names the user and the
// groups alone.
//
// The two unexported fields are the credential's own, and both paths
// set them, so authorize keys its cache on the subject alone and
// never checks which credential arrived. The key is the hash of the
// credential and never the credential, and nothing logs or reports
// it.
type captureSubject struct {
	User   string
	Groups []string
	UID    string
	Extra  map[string][]string

	key    string
	lapses time.Time
}

// unauthenticatedError carries the TokenReview's own words for a token
// it refused. The words reach the client in the error_description of
// the WWW-Authenticate field, RFC 6750 section 3.
type unauthenticatedError struct{ Words string }

func (e unauthenticatedError) Error() string { return e.Words }

// TokenReview is the object the API server answers for a token. This
// API reads authenticated, audiences, error, and the user block.
type TokenReview struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Spec       TokenReviewSpec   `json:"spec"`
	Status     TokenReviewStatus `json:"status,omitempty"`
}

type TokenReviewSpec struct {
	Token     string   `json:"token"`
	Audiences []string `json:"audiences,omitempty"`
}

type TokenReviewStatus struct {
	Authenticated bool            `json:"authenticated"`
	Audiences     []string        `json:"audiences,omitempty"`
	Error         string          `json:"error,omitempty"`
	User          TokenReviewUser `json:"user"`
}

type TokenReviewUser struct {
	Username string              `json:"username,omitempty"`
	UID      string              `json:"uid,omitempty"`
	Groups   []string            `json:"groups,omitempty"`
	Extra    map[string][]string `json:"extra,omitempty"`
}

// SubjectAccessReview asks the question the RBAC rules answer: may this
// subject perform this verb on this resource in this namespace?
type SubjectAccessReview struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Spec       SubjectAccessReviewSpec   `json:"spec"`
	Status     SubjectAccessReviewStatus `json:"status,omitempty"`
}

type SubjectAccessReviewSpec struct {
	ResourceAttributes ResourceAttributes  `json:"resourceAttributes"`
	User               string              `json:"user,omitempty"`
	Groups             []string            `json:"groups,omitempty"`
	UID                string              `json:"uid,omitempty"`
	Extra              map[string][]string `json:"extra,omitempty"`
}

type ResourceAttributes struct {
	Namespace   string `json:"namespace,omitempty"`
	Verb        string `json:"verb"`
	Group       string `json:"group,omitempty"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
}

type SubjectAccessReviewStatus struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// authorizer answers both questions for every request. One lives for
// the life of the process, so its cache serves every request.
type authorizer struct {
	client *Client
	// The clock is a field so a test drives the cache's window
	// without waiting a minute.
	now   func() time.Time
	cache *verdictCache
}

func newAuthorizer(client *Client) *authorizer {
	return &authorizer{
		client: client,
		now:    time.Now,
		cache: &verdictCache{
			subjects: map[string]cachedSubject{},
			grants:   map[string]time.Time{},
		},
	}
}

// authenticate answers who sent the token, from the cache or from a
// TokenReview. A token the review authenticated but whose audiences do
// not carry this API's is refused all the same: the API server
// authenticates a token for the audiences it was minted for, and a
// token minted for another audience was never meant for this API.
func (a *authorizer) authenticate(token string) (captureSubject, error) {
	key := tokenKey(token)
	if subject, ok := a.cache.subject(key, a.now()); ok {
		return subject, nil
	}

	body, err := json.Marshal(&TokenReview{
		APIVersion: "authentication.k8s.io/v1",
		Kind:       "TokenReview",
		Spec:       TokenReviewSpec{Token: token, Audiences: []string{apiAudience}},
	})
	if err != nil {
		return captureSubject{}, err
	}
	reviewed := &TokenReview{}
	if err := a.client.RequestJSON(http.MethodPost, tokenReviewsPath, body, reviewed); err != nil {
		return captureSubject{}, fmt.Errorf("reviewing the token: %w", err)
	}

	if !reviewed.Status.Authenticated {
		return captureSubject{}, unauthenticatedError{
			Words: reviewWords(reviewed.Status.Error, "the token review did not authenticate the token"),
		}
	}
	if !slices.Contains(reviewed.Status.Audiences, apiAudience) {
		return captureSubject{}, unauthenticatedError{
			Words: reviewWords(reviewed.Status.Error, `the token does not carry the audience "`+apiAudience+`"`),
		}
	}

	subject := captureSubject{
		User:   reviewed.Status.User.Username,
		Groups: reviewed.Status.User.Groups,
		UID:    reviewed.Status.User.UID,
		Extra:  reviewed.Status.User.Extra,
		key:    key,
		lapses: a.verdictUntil(token),
	}
	a.cache.keepSubject(key, subject, subject.lapses)
	return subject, nil
}

// reviewWords prefers the review's own words and falls back to a plain
// statement when the review gave none, this repository's error rule.
func reviewWords(refusal, plain string) string {
	if refusal != "" {
		return refusal
	}
	return plain
}

// authorize asks whether the subject may get the named subresource of
// one Player: screen, audio, or media, or the empty string for the
// Player itself. The review names the Player, because an owner may
// grant capture with a rule that carries resourceNames, and a review
// that named the collection would answer for a Player the rule does
// not list. A denial is a false answer, not an error, because the API
// server answered; only a failure to ask is an error.
func (a *authorizer) authorize(subject captureSubject, namespace, name, subresource string) (bool, error) {
	key := grantKey(subject.key, namespace, name, subresource)
	if a.cache.granted(key, a.now()) {
		return true, nil
	}

	body, err := json.Marshal(&SubjectAccessReview{
		APIVersion: "authorization.k8s.io/v1",
		Kind:       "SubjectAccessReview",
		Spec: SubjectAccessReviewSpec{
			ResourceAttributes: ResourceAttributes{
				Namespace:   namespace,
				Verb:        "get",
				Group:       "media.liken.sh",
				Resource:    "players",
				Subresource: subresource,
				Name:        name,
			},
			User:   subject.User,
			Groups: subject.Groups,
			UID:    subject.UID,
			Extra:  subject.Extra,
		},
	})
	if err != nil {
		return false, err
	}
	reviewed := &SubjectAccessReview{}
	if err := a.client.RequestJSON(http.MethodPost, subjectAccessReviewsPath, body, reviewed); err != nil {
		return false, fmt.Errorf("reviewing the subject's access: %w", err)
	}

	// A denial is never cached. It costs one review per refused
	// request, and it means a grant takes effect on the next
	// request rather than a minute later.
	if !reviewed.Status.Allowed {
		return false, nil
	}
	a.cache.keepGrant(key, subject.lapses)
	return true, nil
}

// verdictUntil is when a verdict for this token lapses: the end of the
// window or the token's own expiry, whichever comes first.
func (a *authorizer) verdictUntil(token string) time.Time {
	expiry, _ := tokenExpiry(token)
	return verdictLapse(a.now(), expiry)
}

// verdictLapse is the one rule both credentials lapse by: the end of
// the window, or the credential's own expiry when that comes first. A
// credential with no known expiry keeps the full window.
func verdictLapse(now, expiry time.Time) time.Time {
	until := now.Add(verdictWindow)
	if !expiry.IsZero() && expiry.Before(until) {
		return expiry
	}
	return until
}

// tokenKey is the SHA-256 of the raw token. The cache is keyed by the
// hash, never the token, and nothing logs or reports the key.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// grantKey names one subject's answer for exactly the attributes the
// review asked about: the namespace, the Player, and the subresource.
// The Player is part of the key because a grant carrying resourceNames
// answers for one Player and not for the next one in the same
// namespace.
//
// The first part is the subject's own key, which is the hash of the
// credential that named it.
func grantKey(credential, namespace, name, subresource string) string {
	return credential + "/" + namespace + "/" + name + "/" + subresource
}

// tokenExpiry reads the exp claim out of a JWT without verifying it;
// the TokenReview already did. A token this cannot decode has no known
// expiry and keeps the full window.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	expiry := struct {
		Expires int64 `json:"exp"`
	}{}
	if err := json.Unmarshal(claims, &expiry); err != nil || expiry.Expires == 0 {
		return time.Time{}, false
	}
	return time.Unix(expiry.Expires, 0), true
}

// verdictCache holds the positive verdicts. Every request goroutine
// reads and writes it at once, so it carries its own mutex.
type verdictCache struct {
	mutex    sync.Mutex
	subjects map[string]cachedSubject
	grants   map[string]time.Time
}

type cachedSubject struct {
	subject captureSubject
	until   time.Time
}

func (c *verdictCache) subject(key string, now time.Time) (captureSubject, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	cached, ok := c.subjects[key]
	if !ok || !now.Before(cached.until) {
		return captureSubject{}, false
	}
	return cached.subject, true
}

func (c *verdictCache) keepSubject(key string, subject captureSubject, until time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.subjects[key] = cachedSubject{subject: subject, until: until}
}

func (c *verdictCache) granted(key string, now time.Time) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	until, ok := c.grants[key]
	return ok && now.Before(until)
}

func (c *verdictCache) keepGrant(key string, until time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.grants[key] = until
}
