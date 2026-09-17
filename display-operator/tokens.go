package main

// This file holds the two reviews the API server answers identity
// with, TokenReview and SubjectAccessReview, and the cache that
// holds one verdict. Identity comes from Kubernetes and never from a
// shared secret: a ServiceAccount token names a subject RBAC already
// has rules for, a grant on displays/screen is an ordinary
// ClusterRole, and there is no second identity system to mint,
// rotate, or leak.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// The two audiences. A caller's token names display-api to reach the
// API, and the API's own projected token names display-capture to
// reach a sidecar, so neither token opens the other door.
const (
	apiAudience     = "display-api"
	captureAudience = "display-capture"
)

// The two review kinds and the paths they POST to. Both are creates
// of an object the API server answers in the response and never
// stores, so the API needs create on each and nothing else.
const (
	authenticationAPIVersion = "authentication.k8s.io/v1"
	authorizationAPIVersion  = "authorization.k8s.io/v1"
	tokenReviewsPath         = "/apis/" + authenticationAPIVersion + "/tokenreviews"
	accessReviewsPath        = "/apis/" + authorizationAPIVersion + "/subjectaccessreviews"
)

// The subject a TokenReview answers with. Expires bounds a cache
// entry and nothing else; the API server judged the token's
// validity, not this program.
type reviewedToken struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string][]string
	Expires  time.Time
}

// The TokenReview fields this program writes and reads, the way
// displays.go holds its resource: the token and audiences in the
// spec, and the verdict in the status.
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

// audiences is the field that makes a verdict mean this API: the
// server lists the audiences the token names that the review asked
// for. error carries the API server's own words about a refusal.
type tokenReviewStatus struct {
	Authenticated bool       `json:"authenticated"`
	User          reviewUser `json:"user"`
	Audiences     []string   `json:"audiences,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type reviewUser struct {
	Username string              `json:"username,omitempty"`
	UID      string              `json:"uid,omitempty"`
	Groups   []string            `json:"groups,omitempty"`
	Extra    map[string][]string `json:"extra,omitempty"`
}

// reviewToken sends one TokenReview for one audience.
//
// A verdict is good only when the review both authenticated the token
// and named the audience that was asked for, so a pod's ordinary
// API-server token, whose audience is the API server, does not open
// the door.
func reviewToken(c *Client, token, audience string) (*reviewedToken, *fault) {
	body, err := json.Marshal(tokenReview{
		APIVersion: authenticationAPIVersion,
		Kind:       "TokenReview",
		Spec:       tokenReviewSpec{Token: token, Audiences: []string{audience}},
	})
	if err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}

	answer := &tokenReview{}
	if err := c.RequestJSON(http.MethodPost, tokenReviewsPath, body, answer); err != nil {
		return nil, unavailable(problemUpstreamFailed, err.Error())
	}

	status := answer.Status
	if !status.Authenticated || !slices.Contains(status.Audiences, audience) {
		return nil, unauthenticated(refusedDetail(status, audience))
	}
	return &reviewedToken{
		Username: status.User.Username,
		UID:      status.User.UID,
		Groups:   status.User.Groups,
		Extra:    status.User.Extra,
		Expires:  tokenExpiry(token),
	}, nil
}

// A refusal answers with the review's own words. A review that
// authenticated the token but named no audience carries no error,
// and then the audience the API asked for is the one fact to state.
func refusedDetail(status tokenReviewStatus, audience string) string {
	if status.Error != "" {
		return status.Error
	}
	return "the token does not name the audience " + audience
}

// The exp claim is read out of the payload segment with no signature
// check. Verifying is the API server's job, and this value only
// bounds a cache entry, which the minute bounds as well.
func tokenExpiry(token string) time.Time {
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		return time.Time{}
	}
	claims := struct {
		Expires int64 `json:"exp"`
	}{}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expires == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Expires, 0).UTC()
}

// The cache holds positive verdicts, keyed by the hash of the token,
// and never holds a denial. The clock is a field so a test can move
// it past a verdict's horizon.
type tokenCache struct {
	review func(token string) (*reviewedToken, *fault)
	now    func() time.Time

	mu   sync.Mutex
	held map[string]heldVerdict
}

type heldVerdict struct {
	who   *reviewedToken
	until time.Time
}

// A positive verdict is held for at most a minute, so a deleted
// ServiceAccount stops opening the door within one. A token that
// expires sooner bounds its own entry. Only authentication is held;
// the SubjectAccessReview runs on every request.
const verdictLife = 60 * time.Second

func newTokenCache(review func(token string) (*reviewedToken, *fault)) *tokenCache {
	return &tokenCache{review: review, now: time.Now, held: map[string]heldVerdict{}}
}

// A denial is never held, so a token the API server refused once is
// asked about again on the next request, and a refusal that was an
// API-server hiccup is never remembered as one.
func (t *tokenCache) verdict(token string) (*reviewedToken, *fault) {
	key, now := tokenKey(token), t.now()

	t.mu.Lock()
	held, ok := t.held[key]
	t.mu.Unlock()
	if ok && now.Before(held.until) {
		return held.who, nil
	}

	who, f := t.review(token)
	if f != nil {
		return nil, f
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	// Stale entries are dropped on the way past, so a cache fed
	// one-shot tokens does not grow without end.
	for stale, entry := range t.held {
		if !now.Before(entry.until) {
			delete(t.held, stale)
		}
	}
	t.held[key] = heldVerdict{who: who, until: verdictHorizon(who, now)}
	return who, nil
}

// A verdict outlives neither the minute nor the token itself.
func verdictHorizon(who *reviewedToken, now time.Time) time.Time {
	horizon := now.Add(verdictLife)
	if !who.Expires.IsZero() && who.Expires.Before(horizon) {
		return who.Expires
	}
	return horizon
}

// The key is the SHA-256 of the raw token in hex, so the cache holds
// no token. The API never logs the key and never answers with it.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// The SubjectAccessReview fields this program writes and reads: the
// resource and the subject in the spec, and the verdict with the API
// server's reason in the status.
type subjectAccessReview struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Spec       subjectAccessReviewSpec   `json:"spec"`
	Status     subjectAccessReviewStatus `json:"status,omitempty"`
}

// The four subject fields come from the TokenReview's own status, so
// the API server judges the caller and never the API's own account.
type subjectAccessReviewSpec struct {
	ResourceAttributes resourceAttributes  `json:"resourceAttributes"`
	User               string              `json:"user,omitempty"`
	Groups             []string            `json:"groups,omitempty"`
	UID                string              `json:"uid,omitempty"`
	Extra              map[string][]string `json:"extra,omitempty"`
}

// A Display is cluster-scoped, so its namespace is empty, and the
// field is written as empty rather than left out so the review says
// so.
type resourceAttributes struct {
	Namespace   string `json:"namespace"`
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
}

type subjectAccessReviewStatus struct {
	Allowed         bool   `json:"allowed"`
	Denied          bool   `json:"denied,omitempty"`
	Reason          string `json:"reason,omitempty"`
	EvaluationError string `json:"evaluationError,omitempty"`
}

// authorizeSubject asks the API server whether one subject may take
// one verb on one named resource, or on its subresource.
//
// The subresource exists in no CRD. It is a string RBAC matches, as
// pods/log is, so a ClusterRole names displays/screen and the API
// server answers without any object of that name.
func authorizeSubject(c *Client, who *reviewedToken,
	verb, group, resource, subresource, namespace, name string) (allowed bool, reason string, f *fault) {
	body, err := json.Marshal(subjectAccessReview{
		APIVersion: authorizationAPIVersion,
		Kind:       "SubjectAccessReview",
		Spec: subjectAccessReviewSpec{
			ResourceAttributes: resourceAttributes{
				Namespace:   namespace,
				Verb:        verb,
				Group:       group,
				Resource:    resource,
				Subresource: subresource,
				Name:        name,
			},
			User:   who.Username,
			Groups: who.Groups,
			UID:    who.UID,
			Extra:  who.Extra,
		},
	})
	if err != nil {
		return false, "", unavailable(problemUpstreamFailed, err.Error())
	}

	answer := &subjectAccessReview{}
	if err := c.RequestJSON(http.MethodPost, accessReviewsPath, body, answer); err != nil {
		return false, "", unavailable(problemUpstreamFailed, err.Error())
	}
	return answer.Status.Allowed, verdictReason(answer.Status), nil
}

// The reason carries the API server's own words. An evaluation error
// is more words about the same verdict, not a second verdict, so the
// two are joined.
func verdictReason(status subjectAccessReviewStatus) string {
	if status.EvaluationError == "" {
		return status.Reason
	}
	if status.Reason == "" {
		return status.EvaluationError
	}
	return status.Reason + "; " + status.EvaluationError
}

// servesAccount reports whether an authenticated username is one
// ServiceAccount's, in the form the API server spells it.
func servesAccount(username, namespace, name string) bool {
	return username == "system:serviceaccount:"+namespace+":"+name
}
